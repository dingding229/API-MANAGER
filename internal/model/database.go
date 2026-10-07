package model

import (
	"encoding/json"
	"time"
)

type DatabaseTable struct {
	Types       []string `json:"-"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Columns     []string `json:"columns"`
	Primary     []string `json:"primary"`
	ApproxRows  int64    `json:"approx_rows"`
	Bytes       int64    `json:"bytes"`
	IndexBytes  int64    `json:"index_bytes"`
}
type DatabaseRows struct {
	Table    string           `json:"table"`
	Columns  []string         `json:"columns"`
	Items    []map[string]any `json:"items"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	HasMore  bool             `json:"has_more"`
}
type DatabaseSnapshotTable struct {
	Name    string            `json:"name"`
	Columns []string          `json:"columns"`
	Rows    []json.RawMessage `json:"rows"`
	Nulls   [][]string        `json:"sql_null_columns"`
}
type DatabaseSnapshot struct {
	Format    int                     `json:"format"`
	Schema    string                  `json:"schema"`
	CreatedAt time.Time               `json:"created_at"`
	Tables    []DatabaseSnapshotTable `json:"tables"`
}
