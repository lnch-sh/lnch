package supervisor

import "sync"

type logBuf struct {
	mu    sync.Mutex
	limit int
	lines map[string][]string
}

func (l *logBuf) Add(name, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lines == nil {
		l.lines = map[string][]string{}
	}
	if l.limit == 0 {
		l.limit = 2000
	}
	buf := append(l.lines[name], line)
	if len(buf) > l.limit {
		next := make([]string, l.limit)
		copy(next, buf[len(buf)-l.limit:])
		buf = next
	}
	l.lines[name] = buf
}

func (l *logBuf) Tail(name string, n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	src := l.lines[name]
	if n <= 0 || n > len(src) {
		n = len(src)
	}
	out := make([]string, n)
	copy(out, src[len(src)-n:])
	return out
}

func (l *logBuf) All(n int) map[string][]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string][]string, len(l.lines))
	for name, src := range l.lines {
		take := n
		if take <= 0 || take > len(src) {
			take = len(src)
		}
		cp := make([]string, take)
		copy(cp, src[len(src)-take:])
		out[name] = cp
	}
	return out
}
