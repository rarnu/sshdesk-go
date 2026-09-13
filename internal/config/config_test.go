package config

import (
	"errors"
	"os"
	"testing"
)

func reader(content string) func(string) ([]byte, error) {
	return func(string) ([]byte, error) {
		if content == "" {
			return nil, os.ErrNotExist
		}
		return []byte(content), nil
	}
}

func getenv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func TestDefaultsWithoutFile(t *testing.T) {
	values, err := values("alice", reader(""), getenv(map[string]string{"HOME": "/home/alice"}))
	if err != nil {
		t.Fatal(err)
	}
	if values["DISPLAY"] != ":0" {
		t.Errorf("DISPLAY = %q, want :0", values["DISPLAY"])
	}
	if values["XAUTHORITY"] != "/home/alice/.Xauthority" {
		t.Errorf("XAUTHORITY = %q", values["XAUTHORITY"])
	}
	if values["RUN_AS"] != "alice" {
		t.Errorf("RUN_AS = %q", values["RUN_AS"])
	}
	for _, key := range sshdeskKeys {
		if values[key] != "auto" {
			t.Errorf("%s = %q, want auto", key, values[key])
		}
	}
}

func TestFileOverridesEnvironment(t *testing.T) {
	file := "DISPLAY=:9\nSSHDESK_COLOR=256\nRUN_AS=desktop.user\n"
	env := map[string]string{
		"DISPLAY":       ":3",
		"SSHDESK_COLOR": "truecolor",
		"HOME":          "/home/alice",
	}
	values, err := values("alice", reader(file), getenv(env))
	if err != nil {
		t.Fatal(err)
	}
	if values["DISPLAY"] != ":9" {
		t.Errorf("DISPLAY = %q, want file override :9", values["DISPLAY"])
	}
	if values["SSHDESK_COLOR"] != "256" {
		t.Errorf("SSHDESK_COLOR = %q, want file override 256", values["SSHDESK_COLOR"])
	}
	if values["RUN_AS"] != "desktop.user" {
		t.Errorf("RUN_AS = %q", values["RUN_AS"])
	}
}

func TestEnvironmentUsedWhenFileLacksKey(t *testing.T) {
	env := map[string]string{"DISPLAY": ":7", "HOME": "/home/alice"}
	values, err := values("alice", reader("SSHDESK_MOUSE=0\n"), getenv(env))
	if err != nil {
		t.Fatal(err)
	}
	if values["DISPLAY"] != ":7" {
		t.Errorf("DISPLAY = %q, want env value :7", values["DISPLAY"])
	}
	if values["SSHDESK_MOUSE"] != "0" {
		t.Errorf("SSHDESK_MOUSE = %q", values["SSHDESK_MOUSE"])
	}
}

func TestUnknownKeysAndMalformedLinesIgnored(t *testing.T) {
	file := "MALICIOUS=$(rm -rf /)\nnoequalsign\nDISPLAY=:2\nSHELL=/bin/evil\n"
	values, err := values("alice", reader(file), getenv(map[string]string{"HOME": "/home/alice"}))
	if err != nil {
		t.Fatal(err)
	}
	if values["DISPLAY"] != ":2" {
		t.Errorf("DISPLAY = %q", values["DISPLAY"])
	}
	if _, ok := values["MALICIOUS"]; ok {
		t.Error("unknown key must be ignored")
	}
	if _, ok := values["SHELL"]; ok {
		t.Error("non-whitelisted key must be ignored")
	}
}

func TestRunAsValidation(t *testing.T) {
	for _, bad := range []string{"bad user", "a;b", "root$(x)", "../etc"} {
		_, err := values("alice", reader("RUN_AS="+bad+"\n"), getenv(map[string]string{}))
		if err == nil {
			t.Errorf("RUN_AS %q must be rejected", bad)
		}
	}
	for _, good := range []string{"alice", "desk.top", "user-1_2"} {
		if _, err := values("alice", reader("RUN_AS="+good+"\n"), getenv(map[string]string{})); err != nil {
			t.Errorf("RUN_AS %q must be accepted: %v", good, err)
		}
	}
}

func TestUnreadableFileOtherThanMissingFails(t *testing.T) {
	failing := func(string) ([]byte, error) {
		return nil, errors.New("permission denied")
	}
	if _, err := values("alice", failing, getenv(map[string]string{})); err == nil {
		t.Error("unexpected read errors must propagate")
	}
}

func TestPathLayout(t *testing.T) {
	if got := Path("alice"); got != "/etc/sshdesk/alice.conf" {
		t.Errorf("Path = %q", got)
	}
}
