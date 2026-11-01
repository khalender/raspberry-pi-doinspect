package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	c, err := FromEnv(env(map[string]string{"DOINSPECT_TARGETS": "pi-host=http://192.0.2.10:9100/metrics, opnsense=http://192.0.2.1:9100/metrics"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Interval != 15*time.Second || c.Keep != 168*time.Hour || c.LocalName != "docker-vm" || len(c.Targets) != 2 || c.Targets[1].Name != "opnsense" {
		t.Fatalf("%+v", c)
	}
}

func TestBad(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"DOINSPECT_TARGETS": "pi-host"}, "want name=http"},
		{map[string]string{"DOINSPECT_TARGETS": "a=ftp://x/metrics"}, "want name=http"},
		{map[string]string{"DOINSPECT_TARGETS": "a=http://x:9100,a=http://y:9100"}, "twice"},
		{map[string]string{"DOINSPECT_TARGETS": "docker-vm=http://x:9100"}, "twice"},
		{map[string]string{"DOINSPECT_INTERVAL": "1500ms"}, "whole seconds"},
		{map[string]string{"DOINSPECT_KEEP": "10s"}, "shorter"},
		{map[string]string{"DOINSPECT_LOCAL": "-"}, "nothing to measure"},
	} {
		if _, err := FromEnv(env(tc.env)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: %v, want %q", tc.env, err, tc.want)
		}
	}
}
