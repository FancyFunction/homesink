// Package config loads the Homesink daemon's configuration from the
// environment. There is no config file by design: minimal configuration is a
// product requirement (see the table in WP-B1).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Defaults for every environment variable, kept as exported constants so tests
// and operators can refer to them by name.
const (
	DefaultDataDir       = "/data"
	DefaultAddr          = ":8443"
	DefaultMaxFileBytes  = int64(17179869184) // 16 GiB (D-21)
	DefaultVideoCodec    = "hevc"
	DefaultLogLevel      = "info"
	DefaultTZ            = "Europe/Berlin"
	envDataDir           = "HOMESINK_DATA"
	envAddr              = "HOMESINK_ADDR"
	envTLS               = "HOMESINK_TLS"
	envServerName        = "HOMESINK_SERVER_NAME"
	envMaxFileBytes      = "HOMESINK_MAX_FILE_BYTES"
	envVideoCodec        = "HOMESINK_VIDEO_CODEC"
	envWorkers           = "HOMESINK_WORKERS"
	envKeepOriginalVideo = "HOMESINK_KEEP_ORIGINAL_VIDEOS"
	envUpdateCheck       = "HOMESINK_UPDATE_CHECK"
	envLogLevel          = "HOMESINK_LOG_LEVEL"
	envTZ                = "TZ"
)

// Config is the fully-parsed, validated daemon configuration. Every field is
// resolved: Workers is never 0, Location is never nil.
type Config struct {
	DataDir            string         // HOMESINK_DATA — verified to exist and be writable
	Addr               string         // HOMESINK_ADDR — listen address, "host:port"
	TLS                bool           // HOMESINK_TLS — true = "on", false = "off" → h2c (D-02)
	ServerName         string         // HOMESINK_SERVER_NAME — shown during pairing
	MaxFileBytes       int64          // HOMESINK_MAX_FILE_BYTES — per-file cap (D-21)
	VideoCodec         string         // HOMESINK_VIDEO_CODEC — "hevc" | "h264" (D-29)
	Workers            int            // HOMESINK_WORKERS — resolved job-worker count (D-32)
	KeepOriginalVideos bool           // HOMESINK_KEEP_ORIGINAL_VIDEOS — disables D-30 replacement
	UpdateCheck        bool           // HOMESINK_UPDATE_CHECK — GitHub release poll (D-35)
	LogLevel           slog.Level     // HOMESINK_LOG_LEVEL
	Location           *time.Location // TZ — nightly jobs and log timestamps only (❓5)
	TZName             string         // the raw TZ string, for logging
}

// Getenv is the shape of os.Getenv; injected so tests need no real environment.
type Getenv func(string) string

// Load reads and validates the configuration. env is normally os.Getenv. It
// returns a *Config on success or a single error describing the first invalid
// setting; the error message is safe to print straight to stderr.
func Load(env Getenv) (*Config, error) {
	c := &Config{}

	c.DataDir = orDefault(env(envDataDir), DefaultDataDir)
	if err := checkWritableDir(c.DataDir); err != nil {
		return nil, fmt.Errorf("%s=%q: %w", envDataDir, c.DataDir, err)
	}

	c.Addr = orDefault(env(envAddr), DefaultAddr)
	if err := checkListenAddr(c.Addr); err != nil {
		return nil, fmt.Errorf("%s=%q: %w", envAddr, c.Addr, err)
	}

	tls, err := parseOnOff(env(envTLS), true)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envTLS, err)
	}
	c.TLS = tls

	c.ServerName = orDefault(env(envServerName), hostnameOr("homesink"))

	c.MaxFileBytes, err = parsePositiveInt64(env(envMaxFileBytes), DefaultMaxFileBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envMaxFileBytes, err)
	}

	c.VideoCodec = orDefault(env(envVideoCodec), DefaultVideoCodec)
	if c.VideoCodec != "hevc" && c.VideoCodec != "h264" {
		return nil, fmt.Errorf("%s: want \"hevc\" or \"h264\", got %q", envVideoCodec, c.VideoCodec)
	}

	c.Workers, err = parseWorkers(env(envWorkers))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envWorkers, err)
	}

	c.KeepOriginalVideos, err = parseBool(env(envKeepOriginalVideo), false)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envKeepOriginalVideo, err)
	}

	c.UpdateCheck, err = parseOnOff(env(envUpdateCheck), true)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envUpdateCheck, err)
	}

	c.LogLevel, err = parseLogLevel(orDefault(env(envLogLevel), DefaultLogLevel))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envLogLevel, err)
	}

	c.TZName = orDefault(env(envTZ), DefaultTZ)
	loc, err := time.LoadLocation(c.TZName)
	if err != nil {
		return nil, fmt.Errorf("%s=%q: %w", envTZ, c.TZName, err)
	}
	c.Location = loc

	return c, nil
}

// LogAttrs returns the non-secret configuration as slog attributes for a
// single startup line. No secrets exist in Config, but this keeps the call
// site tidy.
func (c *Config) LogAttrs() []slog.Attr {
	return []slog.Attr{
		slog.String("dataDir", c.DataDir),
		slog.String("addr", c.Addr),
		slog.Bool("tls", c.TLS),
		slog.String("serverName", c.ServerName),
		slog.Int64("maxFileBytes", c.MaxFileBytes),
		slog.String("videoCodec", c.VideoCodec),
		slog.Int("workers", c.Workers),
		slog.Bool("keepOriginalVideos", c.KeepOriginalVideos),
		slog.Bool("updateCheck", c.UpdateCheck),
		slog.String("logLevel", c.LogLevel.String()),
		slog.String("tz", c.TZName),
	}
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}

func hostnameOr(fallback string) string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return fallback
}

// checkWritableDir fails fast when the data directory is missing, is not a
// directory, or cannot be written to (WP-B1 acceptance).
func checkWritableDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("directory does not exist")
		}
		return err
	}
	if !info.IsDir() {
		return errors.New("path is not a directory")
	}
	probe, err := os.CreateTemp(dir, ".homesink-writecheck-*")
	if err != nil {
		return fmt.Errorf("directory is not writable: %w", err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

func checkListenAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("not a valid host:port address: %w", err)
	}
	if port == "" {
		return errors.New("port is required (e.g. \":8443\")")
	}
	if _, perr := net.LookupPort("tcp", port); perr != nil {
		return fmt.Errorf("invalid port %q", port)
	}
	if host != "" {
		if ip := net.ParseIP(host); ip == nil {
			// A non-IP host is allowed (hostname), but reject obvious junk.
			if strings.ContainsAny(host, " \t") {
				return fmt.Errorf("invalid host %q", host)
			}
		}
	}
	return nil
}

func parseOnOff(v string, def bool) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return def, nil
	case "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, fmt.Errorf("want \"on\" or \"off\", got %q", v)
	}
}

func parseBool(v string, def bool) (bool, error) {
	s := strings.TrimSpace(v)
	if s == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return false, fmt.Errorf("want \"true\" or \"false\", got %q", v)
	}
	return b, nil
}

func parsePositiveInt64(v string, def int64) (int64, error) {
	s := strings.TrimSpace(v)
	if s == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("want a positive integer, got %q", v)
	}
	if n <= 0 {
		return 0, fmt.Errorf("want a positive integer, got %d", n)
	}
	return n, nil
}

// parseWorkers resolves 0 (or unset) to max(1, NumCPU/2) per D-32. A negative
// value is an error.
func parseWorkers(v string) (int, error) {
	s := strings.TrimSpace(v)
	n := 0
	if s != "" {
		parsed, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("want a non-negative integer, got %q", v)
		}
		n = parsed
	}
	if n < 0 {
		return 0, fmt.Errorf("want a non-negative integer, got %d", n)
	}
	if n == 0 {
		n = runtime.NumCPU() / 2
		if n < 1 {
			n = 1
		}
	}
	return n, nil
}

func parseLogLevel(v string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("want one of debug|info|warn|error, got %q", v)
	}
}

// LibraryDir is $HOMESINK_DATA/library (00-ARCHITECTURE.md §5).
func (c *Config) LibraryDir() string { return filepath.Join(c.DataDir, "library") }

// InternalDir is $HOMESINK_DATA/.homesink, the machine-owned subtree.
func (c *Config) InternalDir() string { return filepath.Join(c.DataDir, ".homesink") }
