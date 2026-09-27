package routing

import (
	"slices"

	"controlplane/internal/entitlement"
)

type routingLineHop struct {
	Position                       int
	Role, GroupID                  string
	NodeEnabled, GroupEnabled      bool
	ProxyCapable, ForwardCapable   bool
	ProxyPortReady, RelayPortReady bool
}

func routingLineAllowed(owner, line, lineOwner string, hops []routingLineHop, grant entitlement.Snapshot) bool {
	if len(hops) == 0 || len(hops) > 8 || len(hops) > 1 && len(hops) > grant.Limits.MaxHops {
		return false
	}
	for index, hop := range hops {
		expectedRole := "relay"
		if index == 0 && len(hops) > 1 {
			expectedRole = "ingress"
		}
		if index == len(hops)-1 {
			expectedRole = "egress"
		}
		if hop.Position != index || hop.Role != expectedRole || !hop.NodeEnabled || !hop.GroupEnabled ||
			!slices.Contains(grant.ResourceGroupIDs, hop.GroupID) {
			return false
		}
		if len(hops) == 1 {
			if !hop.ProxyCapable || !hop.ProxyPortReady {
				return false
			}
		} else if !hop.ForwardCapable || !hop.RelayPortReady || index == 0 && (!hop.ProxyCapable || !hop.ProxyPortReady) {
			return false
		}
	}
	if lineOwner == "" {
		return slices.Contains(grant.LineIDs, line)
	}
	return lineOwner == owner && grant.Limits.AllowCustomLines
}
