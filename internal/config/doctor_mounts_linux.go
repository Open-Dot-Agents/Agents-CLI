//go:build linux

package config

import "os"

func loadDoctorMounts() ([]doctorMount, error) {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return parseDoctorMounts(string(data))
}
