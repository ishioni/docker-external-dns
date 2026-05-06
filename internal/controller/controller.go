package controller

import (
	"context"
	"log/slog"
	"time"

	"github.com/movishell/docker-external-dns/internal/plan"
	"github.com/movishell/docker-external-dns/internal/provider/unifi"
	"github.com/movishell/docker-external-dns/internal/registry"
	"github.com/movishell/docker-external-dns/internal/source"
)

// Controller orchestrates the reconcile loop.
type Controller struct {
	source   Source
	provider Provider
	ownerID  string
	interval time.Duration
}

func New(src Source, provider Provider, ownerID string, interval time.Duration) *Controller {
	return &Controller{
		source:   src,
		provider: provider,
		ownerID:  ownerID,
		interval: interval,
	}
}

// Run starts the reconcile loop. It blocks until ctx is cancelled.
func (c *Controller) Run(ctx context.Context) {
	slog.Info("starting controller", "owner_id", c.ownerID, "reconcile_interval", c.interval)

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
	log := slog.With("phase", "reconcile")

	desired, err := c.source.Endpoints(ctx)
	if err != nil {
		log.Error("failed to list source endpoints", "err", err)
		return
	}
	log.Debug("desired endpoints", "count", len(desired))

	current, err := c.provider.ListRecords(ctx)
	if err != nil {
		log.Error("failed to list provider records", "err", err)
		return
	}

	changes := plan.Compute(desired, current, c.ownerID)
	log.Info("reconcile plan computed",
		"create", len(changes.Create),
		"update", len(changes.Update),
		"delete", len(changes.Delete),
	)

	created, updated, deleted, failed := 0, 0, 0, 0

	// Create new A + TXT pairs.
	for _, ep := range changes.Create {
		if err := c.createPair(ctx, ep); err != nil {
			log.Error("create failed", "hostname", ep.DNSName, "err", err)
			failed++
		} else {
			created++
		}
	}

	// Update changed A records (and refresh TXT).
	for _, ep := range changes.Update {
		if err := c.updatePair(ctx, ep, current); err != nil {
			log.Error("update failed", "hostname", ep.DNSName, "err", err)
			failed++
		} else {
			updated++
		}
	}

	// Delete stale A + TXT pairs.
	for _, rec := range changes.Delete {
		if err := c.deletePair(ctx, rec, current); err != nil {
			log.Error("delete failed", "hostname", rec.Key, "err", err)
			failed++
		} else {
			deleted++
		}
	}

	log.Info("reconcile done", "created", created, "updated", updated, "deleted", deleted, "failed", failed)
}

func (c *Controller) createPair(ctx context.Context, ep *source.Endpoint) error {
	_, err := c.provider.CreateRecord(ctx, unifi.DNSRecord{
		Key:        ep.DNSName,
		RecordType: ep.RecordType,
		Value:      ep.Target,
	})
	if err != nil {
		return err
	}

	txtKey := registry.TXTKey(ep.RecordType, ep.DNSName)
	_, err = c.provider.CreateRecord(ctx, unifi.DNSRecord{
		Key:        txtKey,
		RecordType: "TXT",
		Value:      registry.EncodeTXT(ep.OwnerID, ep.Resource),
	})
	return err
}

func (c *Controller) updatePair(ctx context.Context, ep *source.Endpoint, current []unifi.DNSRecord) error {
	// Find the existing A record by key to get its ID.
	aRec := findRecord(current, ep.DNSName, ep.RecordType)
	if aRec == nil {
		// Shouldn't happen (plan only adds updates for existing records), but handle gracefully.
		return c.createPair(ctx, ep)
	}
	aRec.Value = ep.Target
	if _, err := c.provider.UpdateRecord(ctx, *aRec); err != nil {
		return err
	}

	// Update the companion TXT record.
	txtKey := registry.TXTKey(ep.RecordType, ep.DNSName)
	txtRec := findRecord(current, txtKey, "TXT")
	newTXT := unifi.DNSRecord{
		Key:        txtKey,
		RecordType: "TXT",
		Value:      registry.EncodeTXT(ep.OwnerID, ep.Resource),
	}
	if txtRec != nil {
		newTXT.ID = txtRec.ID
		_, err := c.provider.UpdateRecord(ctx, newTXT)
		return err
	}
	_, err := c.provider.CreateRecord(ctx, newTXT)
	return err
}

func (c *Controller) deletePair(ctx context.Context, aRec unifi.DNSRecord, current []unifi.DNSRecord) error {
	if err := c.provider.DeleteRecord(ctx, aRec.ID, aRec.Key, aRec.RecordType); err != nil {
		return err
	}

	txtKey := registry.TXTKey(aRec.RecordType, aRec.Key)
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
