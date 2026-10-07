package truenasstore

import "testing"

func TestClassicVMV1SourceContractIsExplicitAndStillOpen(t *testing.T) {
	matrix := loadRuntimeBackendTargetMatrix(t)

	wantAPI := map[string]map[string]string{
		"25.04.2.6": {
			"src/middlewared/middlewared/api/v25_04_2/vm.py":        "201721f7e42e663d11f3e26ff6f59ef2bbce0c5f",
			"src/middlewared/middlewared/api/v25_04_2/vm_device.py": "f85240d8a804b5d8347bc9a51d76c9c93c19a813",
			"src/middlewared/middlewared/plugins/vm/clone.py":       "03d0b4dbad81eada1747308ddd73417e710e29c9",
		},
		"25.10.7": {
			"src/middlewared/middlewared/api/v25_10_5/vm.py":        "2dad2a7e851d354bc9d6e1d201781b2809e77894",
			"src/middlewared/middlewared/api/v25_10_5/vm_device.py": "01250cc4187582f124501b958e512f4db49c561f",
			"src/middlewared/middlewared/plugins/vm/clone.py":        "03d0b4dbad81eada1747308ddd73417e710e29c9",
		},
		"26.0.0-BETA.3": {
			"src/middlewared/middlewared/api/v26_0_0/vm.py":        "0944382fedc0a8cbcfc55fb8a653a9e5758e88fd",
			"src/middlewared/middlewared/api/v26_0_0/vm_device.py": "67603b6e2048da6235b802f970559b627857efc9",
			"src/middlewared/middlewared/plugins/vm/clone.py":      "67e38c9119ff69e6c542b60e89bf5239199baef9",
		},
	}

	requiredMethods := []string{
		"system.version",
		"vm.query",
		"vm.create",
		"vm.update",
		"vm.clone",
		"vm.delete",
		"vm.start",
		"vm.stop",
		"vm.poweroff",
		"vm.status",
		"vm.device.query",
		"vm.device.create",
		"vm.device.delete",
	}

	for version, blobs := range wantAPI {
		cell := matrix.Backends["vm"].TargetStatus[version]
		if cell.Status != "OPEN" || cell.Driver != "vm-v1" || cell.ControlSurface != "vm.*" {
			t.Fatalf("unexpected vm-v1 cell at %s: %#v", version, cell)
		}
		for path, sha := range blobs {
			if got := cell.SourceBlobs[path]; got != sha {
				t.Fatalf("%s source drift at %s: got %q want %q", version, path, got, sha)
			}
		}
		methods := map[string]bool{}
		for _, method := range cell.RequiredMethods {
			methods[method] = true
		}
		for _, method := range requiredMethods {
			if !methods[method] {
				t.Fatalf("%s vm-v1 source contract missing %s", version, method)
			}
		}
		if RuntimeCellOperationallyAdmitted(RuntimeBackendCell{
			Status: cell.Status,
			Driver: cell.Driver,
		}) {
			t.Fatalf("%s vm-v1 must not be operationally admitted before runtime evidence", version)
		}
	}
}

func TestVM25041RemainsNotAdmitted(t *testing.T) {
	matrix := loadRuntimeBackendTargetMatrix(t)
	cell := matrix.Backends["vm"].TargetStatus["25.04.1"]
	if cell.Status != "NOT_ADMITTED" || cell.Driver != "virt-instance-vm-v1" {
		t.Fatalf("25.04.1 VM compatibility path was accidentally admitted: %#v", cell)
	}
}
