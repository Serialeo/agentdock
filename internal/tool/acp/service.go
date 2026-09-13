package acp

import (
	acpruntime "github.com/uvwt/agentdock/internal/acp"
	"github.com/uvwt/agentdock/internal/workspace"
)

type Service struct {
	manager *acpruntime.Manager
	ws      *workspace.Workspace
}

func New(manager *acpruntime.Manager, ws *workspace.Workspace) *Service {
	return &Service{manager: manager, ws: ws}
}

func (s *Service) Close() error {
	if s == nil || s.manager == nil {
		return nil
	}
	return s.manager.Close()
}
