package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// ComplianceVerified tracks real-time NIST 800-53 / OSCAL control compliance per VM.
	// Value is 1 (compliant/verified) or 0 (non-compliant/pending).
	ComplianceVerified = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "uds_vm_compliance_verified",
			Help: "NIST 800-53 / OSCAL compliance verification status (1 = compliant, 0 = non-compliant)",
		},
		[]string{"vm", "namespace", "control"},
	)

	// StorageOverheadRatio tracks the filesystem allocation overhead ratio (allocated PVC size / virtual disk size).
	StorageOverheadRatio = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "uds_vm_storage_overhead_ratio",
			Help: "Calculated PVC storage allocation ratio relative to raw virtual disk size",
		},
		[]string{"vm", "namespace"},
	)

	// PhaseStatus tracks high-cardinality status gauges for VM lifecycle phases.
	PhaseStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "uds_vm_phase_status",
			Help: "Current lifecycle phase of the UDSVirtualMachine (1 for active phase, 0 otherwise)",
		},
		[]string{"vm", "namespace", "phase"},
	)

	// ReconcileTotal tracks the total number of reconciliations by status.
	ReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "uds_vm_reconcile_total",
			Help: "Total number of UDSVirtualMachine reconciliation loops executed",
		},
		[]string{"status"},
	)
)

func init() {
	crmetrics.Registry.MustRegister(
		ComplianceVerified,
		StorageOverheadRatio,
		PhaseStatus,
		ReconcileTotal,
	)
}

// RecordComplianceMetrics updates Prometheus gauges reflecting live compliance state for a VM.
func RecordComplianceMetrics(name, namespace string, checksumVerified, zeroTrust, cloudInitHardened bool, isWindows bool) {
	setGauge := func(control string, verified bool) {
		val := 0.0
		if verified {
			val = 1.0
		}
		ComplianceVerified.WithLabelValues(name, namespace, control).Set(val)
	}

	setGauge("SI-7", checksumVerified)
	setGauge("SC-7", zeroTrust)
	setGauge("AC-3", cloudInitHardened)
	if isWindows {
		// Windows Sysprep & Credential automation
		setGauge("IA-5", true)
	}
}

// RecordPhaseMetrics updates the phase status gauge so only the current phase is 1.
func RecordPhaseMetrics(name, namespace, currentPhase string) {
	phases := []string{"WaitingForUpload", "Provisioning", "Ready", "Failed"}
	for _, p := range phases {
		val := 0.0
		if p == currentPhase {
			val = 1.0
		}
		PhaseStatus.WithLabelValues(name, namespace, p).Set(val)
	}
}

// RecordStorageMetrics records the storage overhead multiplier.
func RecordStorageMetrics(name, namespace string, virtualSizeBytes, allocatedBytes int64) {
	if virtualSizeBytes > 0 && allocatedBytes > 0 {
		ratio := float64(allocatedBytes) / float64(virtualSizeBytes)
		StorageOverheadRatio.WithLabelValues(name, namespace).Set(ratio)
	}
}
