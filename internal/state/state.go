// Package state persists the running stack so other lnch commands can find it.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
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
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(b)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp)
		if werr != nil {
			return werr
		}
		return cerr
	}
	if err := replace(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// replace moves tmp onto path. Windows denies the replace while a reader
// still has path open, so a short retry covers that instead of deleting
// the live file first.
func replace(tmp, path string) error {
	var err error
	for i := 0; i < 20; i++ {
		err = os.Rename(tmp, path)
		if err == nil || !transient(err) {
			return err
		}
		time.Sleep(time.Duration(i+1) * 5 * time.Millisecond)
	}
	return err
}

func readFile(path string) ([]byte, error) {
	var b []byte
	var err error
	for i := 0; i < 20; i++ {
		b, err = os.ReadFile(path)
		if err == nil || !transient(err) {
			return b, err
		}
		time.Sleep(time.Duration(i+1) * 5 * time.Millisecond)
	}
	return nil, err
}

// transient is a Windows sharing or access error from a file that is
// mid-replace. Other systems rename and read without that collision.
func transient(err error) bool {
	if runtime.GOOS != "windows" || err == nil {
		return false
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case 5, 32, 33: // ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION, ERROR_LOCK_VIOLATION
		return true
	default:
		return false
	}
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
	b, err := readFile(path)
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
	b, err := readFile(path)
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
