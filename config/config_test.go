package config

import (
	"testing"
	"time"
)

func TestEnvStr(t *testing.T) {
	t.Setenv("CFG_S", "value")
	if got := envStr("CFG_S", "def"); got != "value" {
		t.Errorf("got %q", got)
	}
	t.Setenv("CFG_S", "")
	if got := envStr("CFG_S", "def"); got != "def" {
		t.Errorf("default fallback: %q", got)
	}
}

func TestEnvInt(t *testing.T) {
	t.Setenv("CFG_I", "42")
	if got := envInt("CFG_I", 7); got != 42 {
		t.Errorf("got %d", got)
	}
	t.Setenv("CFG_I", "notanumber")
	if got := envInt("CFG_I", 7); got != 7 {
		t.Errorf("invalid → default: %d", got)
	}
}

func TestEnvBool(t *testing.T) {
	t.Setenv("CFG_B", "true")
	if !envBool("CFG_B", false) {
		t.Error("true")
	}
	t.Setenv("CFG_B", "false")
	if envBool("CFG_B", true) {
		t.Error("false")
	}
	t.Setenv("CFG_B", "xyz")
	if !envBool("CFG_B", true) {
		t.Error("invalid → default")
	}
}

func TestEnvFloat(t *testing.T) {
	t.Setenv("CFG_F", "3.14")
	if got := envFloat("CFG_F", 1.0); got != 3.14 {
		t.Errorf("got %v", got)
	}
	t.Setenv("CFG_F", "notfloat")
	if got := envFloat("CFG_F", 1.0); got != 1.0 {
		t.Errorf("invalid → default: %v", got)
	}
}

func TestEnvDuration(t *testing.T) {
	t.Setenv("CFG_D", "750ms")
	if got := envDuration("CFG_D", time.Second); got != 750*time.Millisecond {
		t.Errorf("got %v", got)
	}
	t.Setenv("CFG_D", "bad")
	if got := envDuration("CFG_D", time.Second); got != time.Second {
		t.Errorf("invalid → default: %v", got)
	}
}
