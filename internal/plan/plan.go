// Package plan is the launch description shared by detection, config, and the supervisor.
package plan

import "time"

// Plan is everything lnch needs to start a project.
type Plan struct {
	Name     string    `json:"name"`
	Root     string    `json:"root"`
	Source   string    `json:"source"`
	HTTPS    bool      `json:"https"`
	Share    Share     `json:"share"`
	Services []Service `json:"services"`
}

// Share selects the public service and an optional relay.
type Share struct {
	Service string `json:"service,omitempty"`
	Relay   string `json:"relay,omitempty"`
}

// Service is one process in the stack.
type Service struct {
	Name         string            `json:"name"`
	Command      string            `json:"command,omitempty"`
	Argv         []string          `json:"argv,omitempty"`
	Dir          string            `json:"dir"`
	Env          map[string]string `json:"env,omitempty"`
	Port         int               `json:"port,omitempty"`
	Health       string            `json:"health,omitempty"`
	DependsOn    []string          `json:"depends_on,omitempty"`
	Primary      bool              `json:"primary,omitempty"`
	Public       bool              `json:"public,omitempty"`
	Protocol     string            `json:"protocol,omitempty"` // http or tcp
	Restart      string            `json:"restart,omitempty"`  // no or on-failure
	ReadyTimeout time.Duration     `json:"ready_timeout,omitempty"`
	Source       string            `json:"source,omitempty"`
}

// PrimaryName is the service opened and shared by default.
func (p *Plan) PrimaryName() string {
	if p == nil {
		return ""
	}
	if p.Share.Service != "" {
		return p.Share.Service
	}
	for _, s := range p.Services {
		if s.Primary {
			return s.Name
		}
	}
	if len(p.Services) == 1 {
		return p.Services[0].Name
	}
	return ""
}

// AnyPublic reports whether the user marked specific services for sharing.
func (p *Plan) AnyPublic() bool {
	if p == nil {
		return false
	}
	for _, s := range p.Services {
		if s.Public {
			return true
		}
	}
	return false
}
