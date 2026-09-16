//go:build !linux

package config

import "errors"

func loadDoctorMounts() ([]doctorMount, error) {
	return nil, errors.New("mount inspection is available only on Linux")
}
