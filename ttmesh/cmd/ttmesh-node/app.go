package main

import (
    "context"
    "os"
    "time"

    "go.uber.org/zap"

    "ttmesh/pkg/config"
    "ttmesh/pkg/observability"
    "ttmesh/pkg/transport"
    netstack "ttmesh/pkg/core/netstack"
    "ttmesh/pkg/memkv"
    "ttmesh/pkg/peers"
    "ttmesh/pkg/router"
    "ttmesh/pkg/identity"
    "ttmesh/pkg/pipeline"
)

// run is the main entry point after CLI parsing.
func run(opts Options) int {
    cfg, err := config.Load(opts.ConfigPath)
    if err != nil {
        _, _ = os.Stderr.WriteString("failed to load config: " + err.Error() + "\n")
        return 1
    }

    logger, err := observability.SetupLogger(cfg.Log)
    if err != nil {
        _, _ = os.Stderr.WriteString("failed to setup logger: " + err.Error() + "\n")
        return 1
    }
    defer func() { _ = logger.Sync() }()

    // Startup logs + configuration dump
    zap.L().Info("ttmesh-node started", zap.String("app", cfg.AppName))
    zap.L().Info("effective configuration", zap.Any("config", cfg))

    // Load/generate node identity (ed25519)
    priv, canonicalID, err := identity.LoadOrGenEd25519(cfg.Identity)
    if err != nil {
        _, _ = os.Stderr.WriteString("failed to init identity: " + err.Error() + "\n")
        return 1
    }
    if cfg.NodeID == "" || cfg.NodeID == "node-1" {
        // derive NodeID from canonical when not explicitly set
        cfg.NodeID = string(canonicalID)
        zap.L().Info("derived node_id from identity", zap.String("node_id", cfg.NodeID))
    }

    // Create transport manager and start transports from config
    mgr := transport.NewManager()
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    // peer store (in-mem) for basic peer metadata/statistics
    kv := memkv.New(memkv.Options{})
    defer kv.Close()
    ps := peers.NewStore(kv)

    // Initialize router with local node id
    rtr := router.New(ps, mgr, transport.PeerID(cfg.NodeID))
    // Priority pipeline for forwarding
    pl := pipeline.New(rtr, ps)
    defer pl.Close()

    // Build netstack options from config
    nsopts := netstack.Options{
        BackoffInitial: time.Duration(cfg.Net.DialBackoffInitialMS) * time.Millisecond,
        BackoffMax:     time.Duration(cfg.Net.DialBackoffMaxMS) * time.Millisecond,
        BackoffJitter:  time.Duration(cfg.Net.DialBackoffJitterMS) * time.Millisecond,
    }
    _, _, err = netstack.StartFromConfig(ctx, cfg.Transports, mgr, ps, transport.PeerID(cfg.NodeID), rtr, priv, cfg.AppName, pl, nsopts)
    if err != nil {
        zap.L().Error("failed to start transports", zap.Error(err))
        return 1
    }

    zap.L().Info("node is running; press Ctrl+C to exit")
    // Block until process is killed; placeholder run loop.
    select {}
}
