package entitlement

import "slices"

type planLineHop struct {
	Position                       int
	Role, GroupID                  string
	NodeEnabled, GroupEnabled      bool
	ProxyCapable, ForwardCapable   bool
	ProxyPortReady, RelayPortReady bool
}

func planLineGrantAllowed(hops []planLineHop, groupIDs []string, maxHops int) bool {
	if len(hops) == 0 || len(hops) > 8 || len(hops) > maxHops {
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
		if hop.Position != index || hop.Role != expectedRole || !hop.NodeEnabled || !hop.GroupEnabled || !slices.Contains(groupIDs, hop.GroupID) {
			return false
		}
		if len(hops) == 1 {
			if !hop.ProxyCapable || !hop.ProxyPortReady {
				return false
			}
			continue
		}
		if !hop.ForwardCapable || !hop.RelayPortReady || index == 0 && (!hop.ProxyCapable || !hop.ProxyPortReady) {
			return false
		}
	}
	return true
}
