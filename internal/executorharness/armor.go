package executorharness

import (
	"time"

	"github.com/tkdtaylor/agent-builder/internal/armor"
	"github.com/tkdtaylor/agent-builder/internal/ingestion"
)

// ArmorConfig configures an executor harness backed by the armor guard adapter.
type ArmorConfig struct {
	Armor         armor.Config
	BrokerTimeout time.Duration
	Trace         TraceRecorder
}

// NewArmorGuarded constructs an executor harness that reviews every web-content
// and tool-call event with the armor-backed ingestion guard.
func NewArmorGuarded(config ArmorConfig) Harness {
	return New(Config{
		Broker: ingestion.NewBroker(armor.NewGuard(config.Armor), config.BrokerTimeout),
		Trace:  config.Trace,
	})
}
