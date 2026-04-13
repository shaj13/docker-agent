package auto

import (
	"context"
	_ "embed"
	"log/slog"
	"strconv"
	"strings"

	"github.com/docker/docker-agent/pkg/memory/database"
	"github.com/docker/docker-agent/pkg/session"
)

var (
	//go:embed prompts/extract.tpl
	prompt string
)

type progress struct {
	s     *session.Session
	count int
	diff  int
}

type Runner interface {
	Run(context.Context, *session.Session) ([]session.Message, error)
}

func New(db database.Database, r Runner) *Memory {
	return &Memory{
		db:       db,
		r:        r,
		sessionc: make(chan *session.Session, 100),
		extractc: make(chan string, 100),
	}
}

// All msgs should be injected before actual user msg, with.
// <system-reminder>
// each memory should be a usermsg.
type Memory struct {
	db       database.Database
	r        Runner
	sessionc chan *session.Session
	extractc chan string
}

func (m *Memory) Run(ctx context.Context) error {
	session := map[string]*progress{}

	slog.Info("SANAD: Starting memory tool runner")
	
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case s := <-m.sessionc:
			if p, ok := session[s.ID]; ok {
				session[s.ID] = &progress{
					s:     s,
					count: p.count + 1,
					diff:  len(s.GetAllMessages()) - len(p.s.GetAllMessages()),
				}
				continue
			}

			session[s.ID] = &progress{
				s:     s,
				count: 1,
				diff:  1,
			}

			go m.extractMemories(ctx, session[s.ID])

		case id := <-m.extractc:
			p, ok := session[id]
			if !ok {
				continue
			}

			p.count--
			if p.count == 0 {
				delete(session, id)
			}

			go m.extractMemories(ctx, p)
		}
	}
}

func (m *Memory) ExtractMemories(s *session.Session) {
	m.sessionc <- s
}

func (m *Memory) extractMemories(ctx context.Context, p *progress) {
	defer func() {
		m.extractc <- p.s.ID
	}()

	// Here need to check if tool_mem called, then ignore the extraction.

	slog.Debug("Extracting memories", "session_id", p.s.ID, "message_count", p.diff)

	s := session.New(
		session.WithTitle("Extracting memories"),
		session.WithMessages(p.s.Messages),
		session.WithID(p.s.ID),
		session.WithHideToolResults(true),
		session.WithNonInteractive(true),
		session.WithToolsApproved(true),
		session.WithUserMessage(strings.ReplaceAll(prompt, "{{.MessageCount}}", strconv.Itoa(p.diff))),
	)

	_, err := m.r.Run(ctx, s)
	if err != nil {
		slog.Error("failed to extract memories", "error", err, "session_id", p.s.ID)
		return
	}

	slog.Debug("Finished extracting memories", "session_id", p.s.ID, "assistant", s.GetLastAssistantMessageContent())
}
