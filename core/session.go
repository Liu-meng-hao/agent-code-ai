package core

import (
	"github.com/openai/openai-go/v3/responses"
)

type Session struct {
	Messages responses.ResponseInputParam
	Len      int
}

func NewSession() *Session {
	return &Session{
		Messages: responses.ResponseInputParam{},
		Len:      0,
	}
}

func (s *Session) GetMessages() responses.ResponseInputParam {
	return s.Messages
}

func (s *Session) GetLen() int {
	return s.Len
}
func (s *Session) AddMessage(item ...responses.ResponseInputItemUnionParam) {
	s.Messages = append(s.Messages, item...)
	s.Len++
}
