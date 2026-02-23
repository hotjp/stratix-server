package router

import (
	"strings"
	"sync"

	"stratix-server/config"
	"github.com/rs/zerolog/log"
)

type Router struct {
	routes map[string]string // clientID -> gateway URL
	rules  []*config.RouteConfig
	mu     sync.RWMutex
}

func NewRouter(rules []config.RouteConfig) *Router {
	r := &Router{
		routes: make(map[string]string),
		rules:  make([]*config.RouteConfig, 0, len(rules)),
	}

	for i := range rules {
		r.rules = append(r.rules, &rules[i])
	}

	return r
}

func (r *Router) AddRoute(clientId, gatewayUrl string) {
	r.mu.Lock()
	r.routes[clientId] = gatewayUrl
	r.mu.Unlock()
}

func (r *Router) RemoveRoute(clientId string) {
	r.mu.Lock()
	delete(r.routes, clientId)
	r.mu.Unlock()
}

func (r *Router) GetRoute(clientId string) (string, bool) {
	r.mu.RLock()
	if url, ok := r.routes[clientId]; ok {
		r.mu.RUnlock()
		return url, true
	}
	r.mu.RUnlock()

	for _, rule := range r.rules {
		if r.matchPrefix(clientId, rule.ClientIdPrefix) {
			log.Debug().Str("clientId", clientId).Str("rule", rule.ClientIdPrefix).Msg("Route matched")

			r.mu.Lock()
			r.routes[clientId] = rule.OpenclawGateway
			r.mu.Unlock()

			return rule.OpenclawGateway, true
		}
	}

	return "", false
}

func (r *Router) matchPrefix(clientId, prefix string) bool {
	// Simple prefix match
	return strings.HasPrefix(clientId, prefix)
}

func (r *Router) ReloadRoutes(rules []config.RouteConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.routes = make(map[string]string) // Clear cache
	r.rules = make([]*config.RouteConfig, 0, len(rules))

	for i := range rules {
		r.rules = append(r.rules, &rules[i])
	}

	log.Info().Int("rules", len(rules)).Msg("Routes reloaded")
}
