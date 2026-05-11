package controller

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ishioni/dexd/internal/config"
	appmetrics "github.com/ishioni/dexd/internal/metrics"
	"github.com/ishioni/dexd/internal/plan"
	"github.com/ishioni/dexd/internal/provider/unifi"
	"github.com/ishioni/dexd/internal/registry"
	"github.com/ishioni/dexd/internal/source"
)

// Controller orchestrates the reconcile loop.
type Controller struct {
	source    Source
	provider  Provider
	ownerID   string
	txtPrefix string
	policy    config.Policy
	interval  time.Duration
}

func New(src Source, provider Provider, ownerID, txtPrefix string, policy config.Policy, interval time.Duration) *Controller {
	return &Controller{
		source:    src,
		provider:  provider,
		ownerID:   ownerID,
		txtPrefix: txtPrefix,
		policy:    policy,
		interval:  interval,
	}
}

// Run starts the reconcile loop. It blocks until ctx is cancelled.
func (c *Controller) Run(ctx context.Context) {
	slog.Info("starting controller", "owner_id", c.ownerID, "policy", c.policy, "reconcile_interval", c.interval)

	// Initial reconcile.
	c.reconcile(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	eventCh, errCh := c.source.Events(ctx)

	// debounce: accumulate events for 2s before reconciling.
	debounce := time.NewTimer(0)
	debounce.Stop()
	pending := false

	for {
		select {
		case <-ctx.Done():
			slog.Info("controller shutting down")
			return

		case msg, ok := <-eventCh:
			if !ok {
				slog.Warn("source event stream closed, stopping event-driven reconciles")
				eventCh = nil
				continue
			}
			slog.Debug("source event received", "action", msg.Action, "container", msg.Name)
			appmetrics.IncDockerEvent(msg.Action)
			if !pending {
				debounce.Reset(2 * time.Second)
				pending = true
			}

		case err, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			if err != nil && ctx.Err() == nil {
				slog.Error("source event stream error", "err", err)
				appmetrics.IncSourceError("events")
			}

		case <-debounce.C:
			pending = false
			slog.Debug("debounce fired, reconciling")
			c.reconcile(ctx)

		case <-ticker.C:
			slog.Debug("periodic reconcile triggered")
			c.reconcile(ctx)
		}
	}
}

// reconcile fetches desired state from the source and current state from the
// provider, computes the diff, and applies changes.
func (c *Controller) reconcile(ctx context.Context) {
	start := time.Now()
	success := false
	defer func() {
		appmetrics.ObserveReconcile(time.Since(start), success)
	}()

	log := slog.With("phase", "reconcile")

	desired, err := c.source.Endpoints(ctx)
	if err != nil {
		log.Error("failed to list source endpoints", "err", err)
		appmetrics.IncSourceError("list")
		appmetrics.IncReconcileError("source")
		return
	}
	log.Debug("desired endpoints", "count", len(desired))

	current, err := c.provider.ListRecords(ctx)
	if err != nil {
		log.Error("failed to list provider records", "err", err)
		appmetrics.IncReconcileError("provider_list")
		return
	}

	changes := plan.Compute(desired, current, c.ownerID, c.txtPrefix)
	planned := changes
	changes = applyPolicy(changes, c.policy)
	appmetrics.SetPlanMetrics(len(desired), len(current), map[string]int{
		"create":            len(changes.Create),
		"update":            len(changes.Update),
		"replace":           len(changes.Replace),
		"delete":            len(changes.Delete),
		"orphan_txt_delete": len(changes.OrphanTXT),
	})
	log.Info("reconcile plan computed",
		"create", len(changes.Create),
		"update", len(changes.Update),
		"replace", len(changes.Replace),
		"delete", len(changes.Delete),
		"orphan_txt", len(changes.OrphanTXT),
		"policy", c.policy,
		"planned_create", len(planned.Create),
		"planned_update", len(planned.Update),
		"planned_replace", len(planned.Replace),
		"planned_delete", len(planned.Delete),
		"planned_orphan_txt", len(planned.OrphanTXT),
	)

	created, updated, deleted, orphanCleaned, skippedUnsupported, failed := 0, 0, 0, 0, 0, 0

	// Replace owned records whose type changed, e.g. A -> CNAME.
	for _, replacement := range changes.Replace {
		if err := c.validateEndpoint(replacement.Desired); err != nil {
			log.Warn("unsupported DNS record skipped", "operation", "replace", "hostname", replacement.Desired.DNSName, "record_type", replacement.Desired.RecordType, "target", replacement.Desired.Target, "err", err)
			appmetrics.IncProviderError("unsupported")
			skippedUnsupported++
			continue
		}
		if err := c.replacePair(ctx, replacement.Old, replacement.Desired, current); err != nil {
			log.Error("replace failed", "hostname", replacement.Desired.DNSName, "from_type", replacement.Old.RecordType, "to_type", replacement.Desired.RecordType, "err", err)
			appmetrics.ObserveChange("replace", replacement.Desired.RecordType, false)
			appmetrics.IncReconcileError("apply")
			failed++
		} else {
			appmetrics.ObserveChange("replace", replacement.Desired.RecordType, true)
			deleted++
			created++
		}
	}

	// Create new A/CNAME + TXT pairs.
	for _, ep := range changes.Create {
		if err := c.validateEndpoint(ep); err != nil {
			log.Warn("unsupported DNS record skipped", "operation", "create", "hostname", ep.DNSName, "record_type", ep.RecordType, "target", ep.Target, "err", err)
			appmetrics.IncProviderError("unsupported")
			skippedUnsupported++
			continue
		}
		if err := c.createPair(ctx, ep, current); err != nil {
			log.Error("create failed", "hostname", ep.DNSName, "err", err)
			appmetrics.ObserveChange("create", ep.RecordType, false)
			appmetrics.IncReconcileError("apply")
			failed++
		} else {
			appmetrics.ObserveChange("create", ep.RecordType, true)
			created++
		}
	}

	// Update changed A/CNAME records (and refresh TXT).
	for _, ep := range changes.Update {
		if err := c.validateEndpoint(ep); err != nil {
			log.Warn("unsupported DNS record skipped", "operation", "update", "hostname", ep.DNSName, "record_type", ep.RecordType, "target", ep.Target, "err", err)
			appmetrics.IncProviderError("unsupported")
			skippedUnsupported++
			continue
		}
		if err := c.updatePair(ctx, ep, current); err != nil {
			log.Error("update failed", "hostname", ep.DNSName, "err", err)
			appmetrics.ObserveChange("update", ep.RecordType, false)
			appmetrics.IncReconcileError("apply")
			failed++
		} else {
			appmetrics.ObserveChange("update", ep.RecordType, true)
			updated++
		}
	}

	// Delete stale A + TXT pairs.
	for _, rec := range changes.Delete {
		if err := c.deletePair(ctx, rec, current); err != nil {
			log.Error("delete failed", "hostname", rec.Key, "err", err)
			appmetrics.ObserveChange("delete", rec.RecordType, false)
			appmetrics.IncReconcileError("apply")
			failed++
		} else {
			appmetrics.ObserveChange("delete", rec.RecordType, true)
			deleted++
		}
	}

	// Delete orphan TXT records (we own them but no companion A/CNAME and not desired).
	for _, rec := range changes.OrphanTXT {
		if err := c.provider.DeleteRecord(ctx, rec.ID, rec.Key, "TXT"); err != nil {
			log.Error("orphan TXT delete failed", "key", rec.Key, "err", err)
			appmetrics.ObserveChange("orphan_txt_delete", "TXT", false)
			appmetrics.IncReconcileError("apply")
			failed++
		} else {
			appmetrics.ObserveChange("orphan_txt_delete", "TXT", true)
			orphanCleaned++
		}
	}

	success = failed == 0

	log.Info("reconcile done",
		"created", created,
		"updated", updated,
		"deleted", deleted,
		"orphan_txt_deleted", orphanCleaned,
		"skipped_unsupported", skippedUnsupported,
		"failed", failed,
	)
}

func applyPolicy(changes plan.Changes, policy config.Policy) plan.Changes {
	switch policy {
	case config.PolicySync:
		return changes
	case config.PolicyUpsertOnly:
		changes.Delete = nil
		changes.OrphanTXT = nil
		return changes
	case config.PolicyCreateOnly:
		changes.Update = nil
		changes.Replace = nil
		changes.Delete = nil
		changes.OrphanTXT = nil
		return changes
	default:
		return changes
	}
}

func (c *Controller) createPair(ctx context.Context, ep *source.Endpoint, current []unifi.DNSRecord) error {
	if err := c.upsertTXT(ctx, ep, current); err != nil {
		return err
	}

	_, err := c.provider.CreateRecord(ctx, unifi.DNSRecord{
		Key:        ep.DNSName,
		RecordType: ep.RecordType,
		Value:      ep.Target,
	})
	return err
}

func (c *Controller) validateEndpoint(ep *source.Endpoint) error {
	validator, ok := c.provider.(RecordValidator)
	if !ok {
		return nil
	}
	return validator.ValidateRecord(unifi.DNSRecord{
		Key:        ep.DNSName,
		RecordType: ep.RecordType,
		Value:      ep.Target,
	})
}

func (c *Controller) upsertTXT(ctx context.Context, ep *source.Endpoint, current []unifi.DNSRecord) error {
	txtKey := registry.TXTKey(c.txtPrefix, ep.RecordType, ep.DNSName)
	newTXT := unifi.DNSRecord{
		Key:        txtKey,
		RecordType: "TXT",
		Value:      registry.EncodeTXT(ep.OwnerID, ep.Resource),
	}
	// Upsert: if an orphan TXT already exists at this key, update it instead of creating.
	if existing := findRecord(current, txtKey, "TXT"); existing != nil {
		if !registry.IsOwnedBy(existing.Value, c.ownerID) {
			return fmt.Errorf("ownership TXT %s exists but is not owned by %s", txtKey, c.ownerID)
		}
		if c.policy == config.PolicyCreateOnly {
			return nil
		}
		newTXT.ID = existing.ID
		_, err := c.provider.UpdateRecord(ctx, newTXT)
		return err
	}
	_, err := c.provider.CreateRecord(ctx, newTXT)
	return err
}

func (c *Controller) updatePair(ctx context.Context, ep *source.Endpoint, current []unifi.DNSRecord) error {
	// Find the existing A/CNAME record by key to get its ID.
	aRec := findRecord(current, ep.DNSName, ep.RecordType)
	if aRec == nil {
		// Shouldn't happen (plan only adds updates for existing records), but handle gracefully.
		return c.createPair(ctx, ep, current)
	}
	if err := c.upsertTXT(ctx, ep, current); err != nil {
		return err
	}

	aRec.Value = ep.Target
	if _, err := c.provider.UpdateRecord(ctx, *aRec); err != nil {
		return err
	}
	return nil
}

func (c *Controller) replacePair(ctx context.Context, old unifi.DNSRecord, desired *source.Endpoint, current []unifi.DNSRecord) error {
	if err := c.deletePair(ctx, old, current); err != nil {
		return err
	}
	return c.createPair(ctx, desired, current)
}

func (c *Controller) deletePair(ctx context.Context, aRec unifi.DNSRecord, current []unifi.DNSRecord) error {
	if err := c.provider.DeleteRecord(ctx, aRec.ID, aRec.Key, aRec.RecordType); err != nil {
		return err
	}

	txtKey := registry.TXTKey(c.txtPrefix, aRec.RecordType, aRec.Key)
	txtRec := findRecord(current, txtKey, "TXT")
	if txtRec != nil {
		return c.provider.DeleteRecord(ctx, txtRec.ID, txtRec.Key, "TXT")
	}
	return nil
}

// findRecord searches current records for one matching key and recordType.
func findRecord(records []unifi.DNSRecord, key, recordType string) *unifi.DNSRecord {
	for i := range records {
		if records[i].Key == key && records[i].RecordType == recordType {
			return &records[i]
		}
	}
	return nil
}
