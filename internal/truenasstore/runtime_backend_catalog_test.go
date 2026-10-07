package truenasstore

import (
	"errors"
	"testing"
)

func TestResolveRuntimeBackendExactDriverContracts(t *testing.T) {
	tests := []struct {
		backend string
		version string
		driver  string
		status  string
	}{
		{"apps", "TrueNAS-25.04.1", "apps-v1", "PASS"},
		{"container", "25.10.7", "virt-instance-container-v1", "OPEN"},
		{"container", "TrueNAS-26.0.0-BETA.3", "container-v1", "OPEN"},
		{"vm", "25.04.1", "virt-instance-vm-v1", "NOT_ADMITTED"},
		{"vm", "26.0.0-BETA.3", "vm-v1", "OPEN"},
	}
	for _, tt := range tests {
		t.Run(tt.backend+"/"+tt.version, func(t *testing.T) {
			entry, cell, err := ResolveRuntimeBackend(tt.backend, tt.version)
			if err != nil {
				t.Fatal(err)
			}
			if entry.ConfigID != tt.backend {
				t.Fatalf("config id = %q, want %q", entry.ConfigID, tt.backend)
			}
			if cell.Driver != tt.driver || cell.Status != tt.status {
				t.Fatalf("resolved cell = %#v, want driver=%q status=%q", cell, tt.driver, tt.status)
			}
		})
	}
}

func TestRuntimeBackendOperationalAdmissionIsReceiptDriven(t *testing.T) {
	_, apps, err := ResolveRuntimeBackend("apps", "25.10.7")
	if err != nil {
		t.Fatal(err)
	}
	if !RuntimeCellOperationallyAdmitted(apps) {
		t.Fatal("accepted Apps G5 row should be operationally admitted")
	}

	_, container, err := ResolveRuntimeBackend("container", "26.0.0-BETA.3")
	if err != nil {
		t.Fatal(err)
	}
	if RuntimeCellOperationallyAdmitted(container) {
		t.Fatal("OPEN Container source contract must not become operational support")
	}
}

func TestResolveRuntimeBackendFailsClosed(t *testing.T) {
	if _, _, err := ResolveRuntimeBackend("magic", "25.10.7"); !errors.Is(err, ErrRuntimeBackendUnknown) {
		t.Fatalf("unknown backend error = %v", err)
	}
	if _, _, err := ResolveRuntimeBackend("apps", "99.99"); !errors.Is(err, ErrRuntimeTargetUnknown) {
		t.Fatalf("unknown target error = %v", err)
	}
}
