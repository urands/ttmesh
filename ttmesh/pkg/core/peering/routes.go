package peering

import (
    "ttmesh/pkg/peers"
    ttmeshproto "ttmesh/pkg/protocol/proto"
)

func buildRoutesReply(ps *peers.Store) *ttmeshproto.RoutesReply {
    out := &ttmeshproto.RoutesReply{}
    if ps == nil { return out }
    // peers
    ids := ps.ListPeerIDs()
    for _, id := range ids {
        pm, ok := ps.Get(id)
        if !ok { continue }
        out.Peers = append(out.Peers, &ttmeshproto.PeerMeta{Id: string(pm.ID), NodeName: pm.NodeName, Alg: pm.Alg, Pubkey: pm.PublicKey, Addresses: pm.Addresses, Reachable: pm.Reachable, Labels: pm.Labels})
        for _, to := range pm.ConnectedDirectIDs {
            if to == string(pm.ID) { continue }
            out.Adjacency = append(out.Adjacency, &ttmeshproto.PeerAdj{From: string(pm.ID), To: to})
        }
    }
    // route targets and candidates
    targets := ps.ListRouteTargets()
    out.Targets = append(out.Targets, targets...)
    for _, t := range targets {
        cands := ps.GetRouteCandidates(t)
        for _, c := range cands {
            out.Candidates = append(out.Candidates, &ttmeshproto.RouteCandidatePB{
                Target:       t,
                Path:         c.Path,
                NextHop:      c.NextHop,
                Metric:       c.Metric,
                Source:       c.Source,
                LearnedFrom:  c.LearnedFrom,
                Health:       c.Health,
                UpdatedUnixMs: c.Updated,
            })
        }
    }
    return out
}

