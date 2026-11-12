package prompt

import (
	"fmt"
	"strings"
)

// Script is a Prompter for tests: it answers prompts from a queue and records what was asked
// and said.
type Script struct {
	Answers  []string
	Prompts  []string
	Messages []string
}

// NewScript returns a Script that gives these answers in order.
func NewScript(answers ...string) *Script { return &Script{Answers: answers} }

func (s *Script) next(prompt string) (string, error) {
	s.Prompts = append(s.Prompts, prompt)
	if len(s.Answers) == 0 {
		return "", fmt.Errorf("script: no answer left for prompt %q", prompt)
	}
	a := s.Answers[0]
	s.Answers = s.Answers[1:]
	return a, nil
}

func (s *Script) Line(prompt string) (string, error)   { return s.next(prompt) }
func (s *Script) Secret(prompt string) (string, error) { return s.next(prompt) }

func (s *Script) Confirm(prompt string, def bool) (bool, error) {
	a, err := s.next(prompt)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(a) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

func (s *Script) Message(format string, args ...any) {
	s.Messages = append(s.Messages, fmt.Sprintf(format, args...))
}

// Push queues more answers.
func (s *Script) Push(answers ...string) { s.Answers = append(s.Answers, answers...) }
