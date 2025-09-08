package transport

import (
    "context"
    "sort"
    "sync"
    "time"
)

// Manager keeps at most one canonical Session per peer and applies a
// policy to deduplicate concurrent inbound/outbound links.
type Manager struct {
    mu    sync.RWMutex
    peers map[PeerID]*peerEntry
}

type peerEntry struct {
    canonical Session
    // candidates keeps non-canonical sessions to consider for soft failover
    candidates []Session
}

func NewManager() *Manager { return &Manager{peers: make(map[PeerID]*peerEntry)} }

// AddSession registers a new session for a peer and applies the selection
// policy. If the session loses the election, it is closed and returns (false,false,nil).
// If it becomes canonical and replaced an existing one, returns (true,true,old).
// If it becomes canonical without replacement (first), returns (true,false,nil).
func (m *Manager) AddSession(ctx context.Context, s Session) (accepted bool, replaced bool, old Session, err error) {
    pid := s.Peer().ID
    m.mu.Lock()
    defer m.mu.Unlock()

    pe := m.peers[pid]
    if pe == nil {
        pe = &peerEntry{}
        m.peers[pid] = pe
    }

    // No canonical yet
    if pe.canonical == nil {
        pe.canonical = s
        return true, false, nil, nil
    }

    // Decide whether new session is better
    cur := pe.canonical
    if better(s, cur) {
        old = cur
        pe.canonical = s
        // keep old as candidate for a short time before fully closing
        pe.candidates = append(pe.candidates, old)
        // soft close old after a grace period
        go func(old Session) {
            select {
            case <-ctx.Done():
            case <-time.After(500 * time.Millisecond):
            }
            _ = old.Close()
        }(old)
        return true, true, old, nil
    }

    // Otherwise, reject new session politely
    _ = s.Close()
    return false, false, nil, nil
}

// GetSession returns the current canonical session for a peer (if any).
func (m *Manager) GetSession(id PeerID) Session {
    m.mu.RLock()
    defer m.mu.RUnlock()
    if pe := m.peers[id]; pe != nil { return pe.canonical }
    return nil
}

// ClosePeer closes the canonical session for a peer and clears it.
func (m *Manager) ClosePeer(id PeerID) {
    m.mu.Lock()
    defer m.mu.Unlock()
    if pe := m.peers[id]; pe != nil {
        if pe.canonical != nil { _ = pe.canonical.Close() }
        for _, c := range pe.candidates { _ = c.Close() }
        delete(m.peers, id)
    }
}

// ListPeers returns all known peer IDs.
func (m *Manager) ListPeers() []PeerID {
    m.mu.RLock(); defer m.mu.RUnlock()
    out := make([]PeerID, 0, len(m.peers))
    for id := range m.peers { out = append(out, id) }
    sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
    return out
}

// UpdateQuality can be called by transports to update internal ranking hints.
// For now it re-evaluates only when there's a strictly better candidate (not implemented fully).
func (m *Manager) UpdateQuality(_ PeerID, _ Session) {
    // Placeholder for future quality-based soft failover.
}

// Preference order across kinds; higher is better.
func baseRank(k Kind) int {
    switch k {
    case KindMem:
        return 120
    case KindQUICDirect:
        return 100
    case KindWinPipe:
        return 95
    case KindTCPDirect:
        return 90
    case KindQUICHolePunch:
        return 80
    case KindQUICRelay:
        return 70
    case KindTCPRelay:
        return 60
    case KindUDP:
        return 50
    default:
        return 0
    }
}

// better decides whether a should replace b as canonical.
func better(a, b Session) bool {
    ra := baseRank(a.TransportKind())
    rb := baseRank(b.TransportKind())
    if ra != rb { return ra > rb }

    qa := a.Quality()
    qb := b.Quality()
    // Prefer smaller RTT
    if qa.RTT != qb.RTT { return qa.RTT < qb.RTT }
    // Fallback to newer establishment (reduces split-brain on reconnect races)
    return qa.EstablishedAt.After(qb.EstablishedAt)
}

// RebindPeer promotes/moves a canonical session from oldID to newID after
// authentication/handshake when the true identity becomes known.
// If newID already has a canonical session, the policy is applied to decide
// which one should remain; the loser is closed.
// Returns true if the rebind resulted in newID being associated with the moved session.
func (m *Manager) RebindPeer(oldID, newID PeerID) bool {
    if oldID == newID || newID == "" { return false }
    m.mu.Lock()
    defer m.mu.Unlock()

    src := m.peers[oldID]
    if src == nil || src.canonical == nil {
        return false
    }
    moving := src.canonical

    // Remove old mapping
    delete(m.peers, oldID)

    // Update session peer info if possible
    if mp, ok := moving.(interface{ Peer() PeerInfo; SetPeer(PeerInfo) }); ok {
        pi := mp.Peer(); pi.ID = newID; mp.SetPeer(pi)
    }

    dst := m.peers[newID]
    if dst == nil {
        m.peers[newID] = &peerEntry{canonical: moving}
        return true
    }
    if dst.canonical == nil {
        dst.canonical = moving
        return true
    }
    // Decide which session to keep
    if better(moving, dst.canonical) {
        old := dst.canonical
        dst.canonical = moving
        go func() { _ = old.Close() }()
        return true
    }
    // moving loses: close it
    go func() { _ = moving.Close() }()
    return false
}
