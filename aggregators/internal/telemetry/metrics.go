// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License 2.0;
// you may not use this file except in compliance with the Elastic License 2.0.

// Package telemetry holds the logic for emitting telemetry when performing aggregation.
package telemetry

import (
	"context"
	"fmt"

	"github.com/cockroachdb/pebble"
	"go.opentelemetry.io/otel/metric"
)

const (
	bytesUnit = "by"
	countUnit = "1"
)

// Metrics are a collection of metric used to record all the
// measurements for the aggregators. Sync metrics are exposed
// and used by the calling code to record measurements whereas
// async insturments (mainly pebble database metrics) are
// collected by the observer pattern by passing a metrics provider.
type Metrics struct {
	// Synchronous metrics used to record aggregation service
	// measurements.

	RequestsTotal   metric.Int64Counter
	RequestsFailed  metric.Int64Counter
	EventsTotal     metric.Int64Counter
	EventsProcessed metric.Int64Counter
	BytesIngested   metric.Int64Counter

	// Asynchronous metrics used to get pebble metrics and
	// record measurements. These are kept unexported as they are
	// supposed to be updated via the registered callback.

	pebbleFlushes                  metric.Int64ObservableCounter
	pebbleFlushedBytes             metric.Int64ObservableCounter
	pebbleCompactions              metric.Int64ObservableCounter
	pebbleIngestedBytes            metric.Int64ObservableCounter
	pebbleCompactedBytesRead       metric.Int64ObservableCounter
	pebbleCompactedBytesWritten    metric.Int64ObservableCounter
	pebbleMemtableTotalSize        metric.Int64ObservableGauge
	pebbleTotalDiskUsage           metric.Int64ObservableGauge
	pebbleReadAmplification        metric.Int64ObservableGauge
	pebbleNumSSTables              metric.Int64ObservableGauge
	pebbleTableReadersMemEstimate  metric.Int64ObservableGauge
	pebblePendingCompaction        metric.Int64ObservableGauge
	pebbleMarkedForCompactionFiles metric.Int64ObservableGauge
	pebbleKeysTombstones           metric.Int64ObservableGauge

	// registration represents the token for a the configured callback.
	registration metric.Registration
}

type pebbleProvider func() *pebble.Metrics

// NewMetrics returns a new instance of the metrics.
func NewMetrics(provider pebbleProvider, opts ...Option) (*Metrics, error) {
	var err error
	var i Metrics

	cfg := newConfig(opts...)
	meter := cfg.Meter

	// Aggregator metrics
	i.RequestsTotal, err = meter.Int64Counter(
		"aggregator.requests.total",
		metric.WithDescription("Total number of aggregation requests"),
		metric.WithUnit(countUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for requests total: %w", err)
	}
	i.RequestsFailed, err = meter.Int64Counter(
		"aggregator.requests.failed",
		metric.WithDescription("Total number of aggregation requests failed, including partial failures"),
		metric.WithUnit(countUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for requests failed: %w", err)
	}
	i.EventsTotal, err = meter.Int64Counter(
		"aggregator.events.total",
		metric.WithDescription("Total number of APM Events requested for aggregation per aggregation interval"),
		metric.WithUnit(countUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for events total: %w", err)
	}
	i.EventsProcessed, err = meter.Int64Counter(
		"aggregator.events.processed",
		metric.WithDescription("APM Events successfully aggregated by the aggregator per aggregation interval"),
		metric.WithUnit(countUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for events processed: %w", err)
	}
	i.BytesIngested, err = meter.Int64Counter(
		"aggregator.bytes.ingested",
		metric.WithDescription("Number of bytes ingested by the aggregators"),
		metric.WithUnit(bytesUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for bytes processed: %w", err)
	}

	// Pebble metrics
	i.pebbleFlushes, err = meter.Int64ObservableCounter(
		"pebble.flushes",
		metric.WithDescription("Number of memtable flushes to disk"),
		metric.WithUnit(countUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for flushes: %w", err)
	}
	i.pebbleFlushedBytes, err = meter.Int64ObservableCounter(
		"pebble.flushed-bytes",
		metric.WithDescription("Bytes written during flush"),
		metric.WithUnit(bytesUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for flushed bytes: %w", err)
	}
	i.pebbleCompactions, err = meter.Int64ObservableCounter(
		"pebble.compactions",
		metric.WithDescription("Number of table compactions"),
		metric.WithUnit(countUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for compactions: %w", err)
	}
	i.pebbleIngestedBytes, err = meter.Int64ObservableCounter(
		"pebble.ingested-bytes",
		metric.WithDescription("Bytes ingested"),
		metric.WithUnit(bytesUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for ingested bytes: %w", err)
	}
	i.pebbleCompactedBytesRead, err = meter.Int64ObservableCounter(
		"pebble.compacted-bytes-read",
		metric.WithDescription("Bytes read during compaction"),
		metric.WithUnit(bytesUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for compacted bytes read: %w", err)
	}
	i.pebbleCompactedBytesWritten, err = meter.Int64ObservableCounter(
		"pebble.compacted-bytes-written",
		metric.WithDescription("Bytes written during compaction"),
		metric.WithUnit(bytesUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for compacted bytes written: %w", err)
	}
	i.pebbleMemtableTotalSize, err = meter.Int64ObservableGauge(
		"pebble.memtable.total-size",
		metric.WithDescription("Current size of memtable in bytes"),
		metric.WithUnit(bytesUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for memtable size: %w", err)
	}
	i.pebbleTotalDiskUsage, err = meter.Int64ObservableGauge(
		"pebble.disk.usage",
		metric.WithDescription("Total disk usage by pebble, including live and obsolete files"),
		metric.WithUnit(bytesUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for total disk usage: %w", err)
	}
	i.pebbleReadAmplification, err = meter.Int64ObservableGauge(
		"pebble.read-amplification",
		metric.WithDescription("Current read amplification for the db"),
		metric.WithUnit(countUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for read amplification: %w", err)
	}
	i.pebbleNumSSTables, err = meter.Int64ObservableGauge(
		"pebble.num-sstables",
		metric.WithDescription("Current number of storage engine SSTables"),
		metric.WithUnit(countUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for count of sstables: %w", err)
	}
	i.pebbleTableReadersMemEstimate, err = meter.Int64ObservableGauge(
		"pebble.table-readers-mem-estimate",
		metric.WithDescription("Memory used by index and filter blocks"),
		metric.WithUnit(bytesUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for table cache readers: %w", err)
	}
	i.pebblePendingCompaction, err = meter.Int64ObservableGauge(
		"pebble.estimated-pending-compaction",
		metric.WithDescription("Estimated pending compaction bytes"),
		metric.WithUnit(bytesUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for pending compaction: %w", err)
	}
	i.pebbleMarkedForCompactionFiles, err = meter.Int64ObservableGauge(
		"pebble.marked-for-compaction-files",
		metric.WithDescription("Count of SSTables marked for compaction"),
		metric.WithUnit(countUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for compaction marked files: %w", err)
	}
	i.pebbleKeysTombstones, err = meter.Int64ObservableGauge(
		"pebble.keys.tombstone.count",
		metric.WithDescription("Approximate count of delete keys across the storage engine"),
		metric.WithUnit(countUnit),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metric for tombstones: %w", err)
	}

	if err := i.registerCallback(meter, provider); err != nil {
		return nil, fmt.Errorf("failed to register callback: %w", err)
	}
	return &i, nil
}

// CleanUp unregisters any registered callback for collecting async
// measurements.
func (i *Metrics) CleanUp() error {
	if i == nil || i.registration == nil {
		return nil
	}
	if err := i.registration.Unregister(); err != nil {
		return fmt.Errorf("failed to unregister callback: %w", err)
	}
	return nil
}

func (i *Metrics) registerCallback(meter metric.Meter, provider pebbleProvider) (err error) {
	i.registration, err = meter.RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		pm := provider()
		obs.ObserveInt64(i.pebbleMemtableTotalSize, int64(pm.MemTable.Size))
		obs.ObserveInt64(i.pebbleTotalDiskUsage, int64(pm.DiskSpaceUsage()))

		obs.ObserveInt64(i.pebbleFlushes, pm.Flush.Count)
		obs.ObserveInt64(i.pebbleFlushedBytes, int64(pm.Levels[0].BytesFlushed))

		obs.ObserveInt64(i.pebbleCompactions, pm.Compact.Count)
		obs.ObserveInt64(i.pebblePendingCompaction, int64(pm.Compact.EstimatedDebt))
		obs.ObserveInt64(i.pebbleMarkedForCompactionFiles, int64(pm.Compact.MarkedFiles))

		obs.ObserveInt64(i.pebbleTableReadersMemEstimate, pm.TableCache.Size)
		obs.ObserveInt64(i.pebbleKeysTombstones, int64(pm.Keys.TombstoneCount))

		lm := pm.Total()
		obs.ObserveInt64(i.pebbleNumSSTables, lm.NumFiles)
		obs.ObserveInt64(i.pebbleIngestedBytes, int64(lm.BytesIngested))
		obs.ObserveInt64(i.pebbleCompactedBytesRead, int64(lm.BytesRead))
		obs.ObserveInt64(i.pebbleCompactedBytesWritten, int64(lm.BytesCompacted))
		obs.ObserveInt64(i.pebbleReadAmplification, int64(lm.Sublevels))
		return nil
	},
		i.pebbleMemtableTotalSize,
		i.pebbleTotalDiskUsage,
		i.pebbleFlushes,
		i.pebbleFlushedBytes,
		i.pebbleCompactions,
		i.pebbleIngestedBytes,
		i.pebbleCompactedBytesRead,
		i.pebbleCompactedBytesWritten,
		i.pebbleReadAmplification,
		i.pebbleNumSSTables,
		i.pebbleTableReadersMemEstimate,
		i.pebblePendingCompaction,
		i.pebbleMarkedForCompactionFiles,
		i.pebbleKeysTombstones,
	)
	return
}
