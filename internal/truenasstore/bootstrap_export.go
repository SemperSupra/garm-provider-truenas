package truenasstore

// ContainerBootstrapRuntimeCommand exposes the already-qualified container-native
// GARM runner bootstrap program to other TrueNAS runtime backends without
// duplicating credential/JIT handling logic.
func ContainerBootstrapRuntimeCommand() string {
	return containerBootstrapRuntimeCommand()
}
