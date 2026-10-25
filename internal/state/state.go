// Package state persists the running stack so other lnch commands can find it.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"lnch.sh/internal/proc"
)

const (
	Pending    = "pending"
	Starting   = "starting"
	Running    = "running"
	Healthy    = "healthy"
	Unhealthy  = "unhealthy"
	Exited     = "exited"
	Restarting = "restarting"
)

// Runtime is the on-disk record of one project.
type Runtime struct {
	ID         string    `json:"id"`
	Project    string    `json:"project"`
	Root       string    `json:"root"`
	PID        int       `json:"pid"`
	Control    string    `json:"control"`
	Token      string    `json:"token,omitempty"`
	LogFile    string    `json:"log_file,omitempty"`
	Started    time.Time `json:"started"`
	ShareURL   string    `json:"share_url,omitempty"`
	ShareHint  string    `json:"share_hint,omitempty"`
	ShareError string    `json:"share_error,omitempty"`
	Services   []Service `json:"services"`
}

// Service is one process as other commands should see it.
type Service struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Command   string    `json:"command"`
	Dir       string    `json:"dir"`
	PID       int       `json:"pid,omitempty"`
	Port      int       `json:"port,omitempty"`
	URL       string    `json:"url,omitempty"`
	HTTPSURL  string    `json:"https_url,omitempty"`
	Primary   bool      `json:"primary,omitempty"`
	Protocol  string    `json:"protocol,omitempty"`
	LastLine  string    `json:"last_line,omitempty"`
	Started   time.Time `json:"started,omitempty"`
	HealthyAt time.Time `json:"healthy_at,omitempty"`
}

// Public drops the local control token.
func (r Runtime) Public() Runtime {
	r.Token = ""
	return r
}

// Home is ~/.lnch.
func Home() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".lnch"), nil
}

// ProjectID is a stable id for a project directory.
func ProjectID(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:8])
}

// RunPath is the active state file for root.
func RunPath(root string) (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "runs", ProjectID(root)+".json"), nil
}

// PlanPath is where a detached launch reads its plan.
func PlanPath(root string) (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "plans", ProjectID(root)+".json"), nil
}

// LogPath is the detached supervisor log.
func LogPath(root string) (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "logs", ProjectID(root)+".log"), nil
}

// Save writes the runtime atomically.
func Save(rt Runtime) error {
	path, err := RunPath(rt.Root)
	if err != nil {
		return err
	}
	return write(path, rt)
}

func write(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	_ = os.Remove(path)
	return os.Rename(tmp, path)
}

// SavePlan writes a detached launch plan.
func SavePlan(root string, v any) (string, error) {
	path, err := PlanPath(root)
	if err != nil {
		return "", err
	}
	if err := write(path, v); err != nil {
		return "", err
	}
	return path, nil
}

// Load reads the state file.
func Load(root string) (Runtime, error) {
	path, err := RunPath(root)
	if err != nil {
		return Runtime{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Runtime{}, err
	}
	var rt Runtime
	if err := json.Unmarshal(b, &rt); err != nil {
		return Runtime{}, err
	}
	return rt, nil
}

// LoadPlan reads a JSON plan file.
func LoadPlan(path string, dest any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dest)
}

// LoadIfRunning returns state when its supervisor process is still alive.
func LoadIfRunning(root string) (Runtime, bool) {
	rt, err := Load(root)
	if err != nil {
		return Runtime{}, false
	}
	if rt.PID > 0 && proc.Alive(rt.PID) {
		return rt, true
	}
	_ = Remove(root)
	return Runtime{}, false
}

// Remove deletes the active state file.
func Remove(root string) error {
	path, err := RunPath(root)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Primary returns the service lnch should open.
func Primary(rt Runtime) (Service, bool) {
	for _, s := range rt.Services {
		if s.Primary {
			return s, true
		}
	}
	for _, s := range rt.Services {
		if s.URL != "" || s.HTTPSURL != "" {
			return s, true
		}
	}
	if len(rt.Services) > 0 {
		return rt.Services[0], true
	}
	return Service{}, false
}

// BestLocal is the URL a developer opens on this machine.
func BestLocal(s Service) string {
	if s.HTTPSURL != "" {
		return s.HTTPSURL
	}
	return s.URL
}

// OpenURL is the local URL for the primary service.
func OpenURL(rt Runtime) string {
	s, ok := Primary(rt)
	if !ok {
		return ""
	}
	return BestLocal(s)
}

// ReadyEnough is true when every service is serving or has exited.
func ReadyEnough(rt Runtime) bool {
	if len(rt.Services) == 0 {
		return false
	}
	for _, s := range rt.Services {
		switch s.Status {
		case Healthy, Running, Unhealthy, Exited:
		default:
			return false
		}
	}
	return true
}
