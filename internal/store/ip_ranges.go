package store

import "encoding/json"

func ipRangesJSON(ranges []string) []byte {
	if ranges == nil {
		ranges = []string{}
	}
	v, _ := json.Marshal(ranges)
	return v
}
