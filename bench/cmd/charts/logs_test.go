package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLogsPoolEachVariantOverItsOwnCases(t *testing.T) {
	var lb logbench
	err := json.Unmarshal([]byte(`{"tokens":"o200k_base","variants":[{"key":"raw","label":"raw output"},{"key":"lx","label":"lx"},{"key":"lx-laya","label":"lx + laya"}],
	"cases":[
	 {"id":"A","source":"loghub","templates":10,"variants":[{"key":"raw","tokens":1000,"templates_covered":10},{"key":"lx","tokens":100,"templates_covered":5},{"key":"lx-laya","tokens":100,"templates_covered":5}]},
	 {"id":"B","source":"loghub","templates":30,"variants":[{"key":"raw","tokens":3000,"templates_covered":30},{"key":"lx","tokens":300,"templates_covered":3}]}]}`), &lb)
	if err != nil {
		t.Fatal(err)
	}
	svg := logsH2H(lb)
	for _, want := range []string{"lx: 20.0% of templates, 90.0% of tokens saved", "lx + laya: 50.0% of templates, 90.0% of tokens saved"} {
		if !strings.Contains(svg, want) {
			t.Errorf("no %q", want)
		}
	}
}
