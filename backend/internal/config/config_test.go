package config_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/FancyFunction/homesink/backend/internal/config"
)

// envFrom returns a config.Getenv backed by m.
func envFrom(m map[string]string) config.Getenv {
	return func(k string) string { return m[k] }
}

// baseEnv is a fully-valid environment with a writable data dir; individual
// tests override one key at a time.
func baseEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{"HOMESINK_DATA": t.TempDir()}
}

// --- Acceptance: missing/unwritable HOMESINK_DATA exits non-zero with a readable message ---

func TestLoad_MissingDataDirIsAReadableError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := config.Load(envFrom(map[string]string{"HOMESINK_DATA": missing}))
	if err == nil {
		t.Fatal("expected an error for a missing data dir, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"HOMESINK_DATA", missing, "does not exist"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q missing %q", msg, want)
		}
	}
}

func TestLoad_DataDirIsAFileNotADirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(envFrom(map[string]string{"HOMESINK_DATA": f}))
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("want 'not a directory' error, got %v", err)
	}
}

func TestLoad_UnwritableDataDirIsAReadableError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory mode bits do not restrict writes")
	}
	dir := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err := config.Load(envFrom(map[string]string{"HOMESINK_DATA": dir}))
	if err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("want 'not writable' error, got %v", err)
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error %q should name the offending path %q", err, dir)
	}
}

// --- Acceptance: every env var has a table-driven parse test incl. the invalid case ---

func TestLoad_EnvVarParsing(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr bool
		check   func(t *testing.T, c *config.Config)
	}{
		// HOMESINK_ADDR
		{name: "addr default", key: "", value: "", check: func(t *testing.T, c *config.Config) {
			if c.Addr != config.DefaultAddr {
				t.Errorf("Addr = %q, want %q", c.Addr, config.DefaultAddr)
			}
		}},
		{name: "addr valid", key: "HOMESINK_ADDR", value: "127.0.0.1:9000", check: func(t *testing.T, c *config.Config) {
			if c.Addr != "127.0.0.1:9000" {
				t.Errorf("Addr = %q", c.Addr)
			}
		}},
		{name: "addr invalid: no port", key: "HOMESINK_ADDR", value: "localhost", wantErr: true},
		{name: "addr invalid: bad port", key: "HOMESINK_ADDR", value: ":not-a-port", wantErr: true},

		// HOMESINK_TLS
		{name: "tls default on", key: "", value: "", check: func(t *testing.T, c *config.Config) {
			if !c.TLS {
				t.Error("TLS default should be true")
			}
		}},
		{name: "tls off", key: "HOMESINK_TLS", value: "off", check: func(t *testing.T, c *config.Config) {
			if c.TLS {
				t.Error("TLS should be false for 'off'")
			}
		}},
		{name: "tls invalid", key: "HOMESINK_TLS", value: "maybe", wantErr: true},

		// HOMESINK_SERVER_NAME
		{name: "server name override", key: "HOMESINK_SERVER_NAME", value: "Wohnzimmer-NAS", check: func(t *testing.T, c *config.Config) {
			if c.ServerName != "Wohnzimmer-NAS" {
				t.Errorf("ServerName = %q", c.ServerName)
			}
		}},
		{name: "server name default is non-empty", key: "", value: "", check: func(t *testing.T, c *config.Config) {
			if c.ServerName == "" {
				t.Error("ServerName default should not be empty")
			}
		}},

		// HOMESINK_MAX_FILE_BYTES
		{name: "max file bytes default", key: "", value: "", check: func(t *testing.T, c *config.Config) {
			if c.MaxFileBytes != config.DefaultMaxFileBytes {
				t.Errorf("MaxFileBytes = %d", c.MaxFileBytes)
			}
		}},
		{name: "max file bytes valid", key: "HOMESINK_MAX_FILE_BYTES", value: "1048576", check: func(t *testing.T, c *config.Config) {
			if c.MaxFileBytes != 1048576 {
				t.Errorf("MaxFileBytes = %d", c.MaxFileBytes)
			}
		}},
		{name: "max file bytes invalid: not a number", key: "HOMESINK_MAX_FILE_BYTES", value: "big", wantErr: true},
		{name: "max file bytes invalid: zero", key: "HOMESINK_MAX_FILE_BYTES", value: "0", wantErr: true},
		{name: "max file bytes invalid: negative", key: "HOMESINK_MAX_FILE_BYTES", value: "-5", wantErr: true},

		// HOMESINK_VIDEO_CODEC
		{name: "video codec default hevc", key: "", value: "", check: func(t *testing.T, c *config.Config) {
			if c.VideoCodec != "hevc" {
				t.Errorf("VideoCodec = %q", c.VideoCodec)
			}
		}},
		{name: "video codec h264", key: "HOMESINK_VIDEO_CODEC", value: "h264", check: func(t *testing.T, c *config.Config) {
			if c.VideoCodec != "h264" {
				t.Errorf("VideoCodec = %q", c.VideoCodec)
			}
		}},
		{name: "video codec invalid", key: "HOMESINK_VIDEO_CODEC", value: "av1", wantErr: true},

		// HOMESINK_WORKERS
		{name: "workers default resolves to >=1", key: "", value: "", check: func(t *testing.T, c *config.Config) {
			if c.Workers < 1 {
				t.Errorf("Workers = %d, want >= 1", c.Workers)
			}
			if want := max(1, runtime.NumCPU()/2); c.Workers != want {
				t.Errorf("Workers = %d, want %d", c.Workers, want)
			}
		}},
		{name: "workers zero resolves like unset", key: "HOMESINK_WORKERS", value: "0", check: func(t *testing.T, c *config.Config) {
			if c.Workers != max(1, runtime.NumCPU()/2) {
				t.Errorf("Workers = %d", c.Workers)
			}
		}},
		{name: "workers explicit", key: "HOMESINK_WORKERS", value: "3", check: func(t *testing.T, c *config.Config) {
			if c.Workers != 3 {
				t.Errorf("Workers = %d", c.Workers)
			}
		}},
		{name: "workers invalid: not a number", key: "HOMESINK_WORKERS", value: "many", wantErr: true},
		{name: "workers invalid: negative", key: "HOMESINK_WORKERS", value: "-1", wantErr: true},

		// HOMESINK_KEEP_ORIGINAL_VIDEOS
		{name: "keep original default false", key: "", value: "", check: func(t *testing.T, c *config.Config) {
			if c.KeepOriginalVideos {
				t.Error("KeepOriginalVideos default should be false")
			}
		}},
		{name: "keep original true", key: "HOMESINK_KEEP_ORIGINAL_VIDEOS", value: "true", check: func(t *testing.T, c *config.Config) {
			if !c.KeepOriginalVideos {
				t.Error("KeepOriginalVideos should be true")
			}
		}},
		{name: "keep original invalid", key: "HOMESINK_KEEP_ORIGINAL_VIDEOS", value: "yes-please", wantErr: true},

		// HOMESINK_UPDATE_CHECK
		{name: "update check default on", key: "", value: "", check: func(t *testing.T, c *config.Config) {
			if !c.UpdateCheck {
				t.Error("UpdateCheck default should be true")
			}
		}},
		{name: "update check off", key: "HOMESINK_UPDATE_CHECK", value: "off", check: func(t *testing.T, c *config.Config) {
			if c.UpdateCheck {
				t.Error("UpdateCheck should be false for 'off'")
			}
		}},
		{name: "update check invalid", key: "HOMESINK_UPDATE_CHECK", value: "sometimes", wantErr: true},

		// HOMESINK_LOG_LEVEL
		{name: "log level default info", key: "", value: "", check: func(t *testing.T, c *config.Config) {
			if c.LogLevel != slog.LevelInfo {
				t.Errorf("LogLevel = %v", c.LogLevel)
			}
		}},
		{name: "log level debug", key: "HOMESINK_LOG_LEVEL", value: "debug", check: func(t *testing.T, c *config.Config) {
			if c.LogLevel != slog.LevelDebug {
				t.Errorf("LogLevel = %v", c.LogLevel)
			}
		}},
		{name: "log level warn", key: "HOMESINK_LOG_LEVEL", value: "warn", check: func(t *testing.T, c *config.Config) {
			if c.LogLevel != slog.LevelWarn {
				t.Errorf("LogLevel = %v", c.LogLevel)
			}
		}},
		{name: "log level invalid", key: "HOMESINK_LOG_LEVEL", value: "chatty", wantErr: true},

		// TZ
		{name: "tz default Europe/Berlin", key: "", value: "", check: func(t *testing.T, c *config.Config) {
			if c.TZName != config.DefaultTZ || c.Location == nil || c.Location.String() != "Europe/Berlin" {
				t.Errorf("TZ = %q / %v", c.TZName, c.Location)
			}
		}},
		{name: "tz valid override", key: "TZ", value: "UTC", check: func(t *testing.T, c *config.Config) {
			if c.Location == nil || c.Location.String() != "UTC" {
				t.Errorf("Location = %v", c.Location)
			}
		}},
		{name: "tz invalid", key: "TZ", value: "Mars/Olympus_Mons", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv(t)
			if tc.key != "" {
				env[tc.key] = tc.value
			}
			c, err := config.Load(envFrom(env))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Load() = nil error, want error for %s=%q", tc.key, tc.value)
				}
				if tc.key != "" && !strings.Contains(err.Error(), tc.key) {
					t.Errorf("error %q should name the variable %q", err, tc.key)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, c)
			}
		})
	}
}

func TestLoad_AllDefaults(t *testing.T) {
	c, err := config.Load(envFrom(baseEnv(t)))
	if err != nil {
		t.Fatalf("Load() with only HOMESINK_DATA set: %v", err)
	}
	if c.Addr != config.DefaultAddr || !c.TLS || c.VideoCodec != config.DefaultVideoCodec ||
		!c.UpdateCheck || c.KeepOriginalVideos || c.LogLevel != slog.LevelInfo {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if c.LibraryDir() != filepath.Join(c.DataDir, "library") {
		t.Errorf("LibraryDir() = %q", c.LibraryDir())
	}
	if c.InternalDir() != filepath.Join(c.DataDir, ".homesink") {
		t.Errorf("InternalDir() = %q", c.InternalDir())
	}
	if len(c.LogAttrs()) == 0 {
		t.Error("LogAttrs() should not be empty")
	}
}
