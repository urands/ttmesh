// Package observability contains logging setup and other observability utilities.
package observability

import (
    "os"
    "strings"

    "go.uber.org/zap"
    "go.uber.org/zap/zapcore"
    "gopkg.in/natefinch/lumberjack.v2"

    "ttmesh/pkg/config"
)

// SetupLogger builds a zap.Logger from the provided configuration, sets it as
// the global logger, and redirects the stdlib log package. The caller should
// defer logger.Sync().
func SetupLogger(c config.LogConfig) (*zap.Logger, error) {
    level := zap.NewAtomicLevel()
    switch strings.ToLower(c.Level) {
    case "debug":
        level.SetLevel(zap.DebugLevel)
    case "info":
        level.SetLevel(zap.InfoLevel)
    case "warn", "warning":
        level.SetLevel(zap.WarnLevel)
    case "error":
        level.SetLevel(zap.ErrorLevel)
    default:
        level.SetLevel(zap.InfoLevel)
    }

    encCfg := defaultEncoderConfig(c.Development)
    var encoder zapcore.Encoder
    if strings.ToLower(c.Format) == "json" {
        encoder = zapcore.NewJSONEncoder(encCfg)
    } else {
        encoder = zapcore.NewConsoleEncoder(encCfg)
    }

    var cores []zapcore.Core
    for _, out := range c.Outputs {
        switch strings.ToLower(out) {
        case "stdout":
            cores = append(cores, zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), level))
        case "stderr":
            cores = append(cores, zapcore.NewCore(encoder, zapcore.AddSync(os.Stderr), level))
        default:
            // Treat as file path; use rotation only when enabled
            var ws zapcore.WriteSyncer
            if c.Rotation.Enable {
                ws = zapcore.AddSync(&lumberjack.Logger{
                    Filename:   chooseFilename(out, c),
                    MaxSize:    max(c.Rotation.MaxSizeMB, 10),
                    MaxBackups: max(c.Rotation.MaxBackups, 1),
                    MaxAge:     max(c.Rotation.MaxAgeDays, 7),
                    Compress:   c.Rotation.Compress,
                })
            } else {
                // Ensure directory exists
                if dir := dirOf(out); dir != "" {
                    _ = os.MkdirAll(dir, 0o755)
                }
                f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
                if err != nil {
                    // fallback to stderr on failure
                    ws = zapcore.AddSync(os.Stderr)
                } else {
                    ws = zapcore.AddSync(f)
                }
            }
            cores = append(cores, zapcore.NewCore(encoder, ws, level))
        }
    }

    core := zapcore.NewTee(cores...)
    opts := []zap.Option{
        zap.AddCaller(),
        zap.AddStacktrace(zap.ErrorLevel),
    }
    if c.Development {
        opts = append(opts, zap.Development())
    }

    logger := zap.New(core, opts...)
    zap.ReplaceGlobals(logger)
    // redirect stdlib log to zap at Info level
    _, _ = zap.RedirectStdLogAt(logger, zap.InfoLevel)
    return logger, nil
}

func defaultEncoderConfig(dev bool) zapcore.EncoderConfig {
    if dev {
        cfg := zap.NewDevelopmentEncoderConfig()
        cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
        return cfg
    }
    return zap.NewProductionEncoderConfig()
}

func max(a, b int) int {
    if a > b {
        return a
    }
    return b
}

// chooseFilename returns the output filename. If rotation is enabled and a
// filename is provided in rotation config, prefer it; otherwise use the `out`.
func chooseFilename(out string, c config.LogConfig) string {
    if c.Rotation.Enable && strings.TrimSpace(c.Rotation.Filename) != "" {
        return c.Rotation.Filename
    }
    return out
}

func dirOf(path string) string {
    i := strings.LastIndexAny(path, "/\\")
    if i <= 0 {
        return ""
    }
    return path[:i]
}
