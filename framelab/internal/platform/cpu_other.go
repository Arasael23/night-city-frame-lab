//go:build !windows

package platform

import "os"

// Dev stand-in: a CPU with one kind of core, or a hybrid one with FRAMELAB_FAKE_CPU=hybrid.
func CPU() CPUInfo {
	if os.Getenv("FRAMELAB_FAKE_CPU") == "hybrid" {
		return CPUInfo{Name: "Intel Core i7-14700K", Cores: 20, PCores: 8, ECores: 12, Hybrid: true}
	}
	return CPUInfo{Name: cpuName("AMD Ryzen 7 7800X3D 8-Core Processor"), Cores: 8, PCores: 8, Hybrid: false}
}

func PinGame() int { return 0 }
func UnpinGame()   {}
