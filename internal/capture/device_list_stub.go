//go:build !cgo

package capture

import "fmt"

// ListLoopbackDevices — заглушка без CGO.
func ListLoopbackDevices() ([]string, error) {
	return nil, fmt.Errorf("список устройств недоступен без CGO (нужен GCC)")
}

// ListCaptureDevices — заглушка без CGO.
func ListCaptureDevices() ([]string, error) {
	return nil, fmt.Errorf("список устройств недоступен без CGO (нужен GCC)")
}

// ListAllDevices — заглушка без CGO.
func ListAllDevices() (map[string][]string, error) {
	return nil, fmt.Errorf("список устройств недоступен без CGO (нужен GCC)")
}

// ValidateDevice — заглушка без CGO.
func ValidateDevice(kind int, name string) error {
	if name == "" {
		return nil
	}
	return fmt.Errorf("проверка устройств недоступна без CGO (нужен GCC). Задано: %q", name)
}
