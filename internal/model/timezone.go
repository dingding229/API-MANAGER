package model

import (
	"time"
	_ "time/tzdata"
)

const DefaultTimeZone = "Asia/Shanghai"

func NormalizeTimeZone(name string) (string, error) {
	if name == "" {
		name = DefaultTimeZone
	}
	_, err := time.LoadLocation(name)
	return name, err
}
