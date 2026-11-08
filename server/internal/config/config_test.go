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

func TestNetwork(t *testing.T) {
	c, err := FromEnv(env(map[string]string{
		"DOINSPECT_PING": "gateway=192.0.2.1, internet=1.1.1.1",
		"DOINSPECT_DNS":  "pihole=192.0.2.53:53",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Ping) != 2 || c.Ping[1].URL != "1.1.1.1" || len(c.DNS) != 1 || c.DNSName != "cloudflare.com" {
		t.Fatalf("%+v", c)
	}
	if c.SpeedEvery != 6*time.Hour || c.SpeedAt != 3*time.Hour || c.SpeedBytes != 100_000_000 || c.SpeedPingName != "internet" {
		t.Fatalf("speed test defaults: %+v", c)
	}
	c, err = FromEnv(env(map[string]string{"DOINSPECT_SPEEDTEST_EVERY": "0", "DOINSPECT_SPEEDTEST_AT": "21:30"}))
	if err != nil || c.SpeedEvery != 0 || c.SpeedAt != 21*time.Hour+30*time.Minute {
		t.Fatalf("%v %+v", err, c)
	}
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"DOINSPECT_PING": "gateway"}, "name=address"},
		{map[string]string{"DOINSPECT_PING": "a=1.1.1.1,a=8.8.8.8"}, "twice"},
		{map[string]string{"DOINSPECT_DNS": "pihole=192.0.2.53"}, "host:53"},
		{map[string]string{"DOINSPECT_SPEEDTEST_EVERY": "30s"}, "between"},
		{map[string]string{"DOINSPECT_SPEEDTEST_AT": "3am"}, "03:00"},
		{map[string]string{"DOINSPECT_SPEEDTEST_BYTES": "10"}, "at least"},
	} {
		if _, err := FromEnv(env(tc.env)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: %v, want %q", tc.env, err, tc.want)
		}
	}
}
