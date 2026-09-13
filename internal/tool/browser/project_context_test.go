package browser

import (
	"context"
	"errors"
	"testing"

	protocol "github.com/Serialeo/agentdock-protocol"
	projectstate "github.com/uvwt/agentdock/internal/project"
)

func TestProjectBrowserSessionOwnershipRejectsCrossTargetAccess(t *testing.T) {
	service := New(Config{AgentDockHome: t.TempDir()}, nil)
	service.sessions["browser-1"] = &session{
		id: "browser-1",
		projectOwner: projectOwner{
			WorkSessionID: "ws-1",
			TargetID:      "target-1",
			ProjectID:     "project-1",
			DeploymentID:  "deployment-1",
		},
	}

	ctxOwner := projectstate.WithExecution(context.Background(), projectstate.Execution{
		Target: projectstate.TargetBinding{
			WorkSessionID: "ws-1", TargetID: "target-1", ProjectID: "project-1", DeploymentID: "deployment-1",
		},
	})
	if err := service.requireProjectSessionOwner(ctxOwner, "browser-1"); err != nil {
		t.Fatalf("owner browser access rejected: %v", err)
	}

	ctxOther := projectstate.WithExecution(context.Background(), projectstate.Execution{
		Target: projectstate.TargetBinding{
			WorkSessionID: "ws-2", TargetID: "target-2", ProjectID: "project-1", DeploymentID: "deployment-1",
		},
	})
	err := service.requireProjectSessionOwner(ctxOther, "browser-1")
	var browserErr *Error
	if !errors.As(err, &browserErr) || browserErr.Code != protocol.ErrorSessionTargetDenied {
		t.Fatalf("cross-Target browser error = %#v, want %s", err, protocol.ErrorSessionTargetDenied)
	}
}

func TestProjectBrowserTargetCannotClaimUnownedInternalSession(t *testing.T) {
	service := New(Config{AgentDockHome: t.TempDir()}, nil)
	service.sessions["internal-browser"] = &session{id: "internal-browser"}
	ctx := projectstate.WithExecution(context.Background(), projectstate.Execution{
		Target: projectstate.TargetBinding{
			WorkSessionID: "ws-1", TargetID: "target-1", ProjectID: "project-1", DeploymentID: "deployment-1",
		},
	})
	err := service.requireProjectSessionOwner(ctx, "internal-browser")
	var browserErr *Error
	if !errors.As(err, &browserErr) || browserErr.Code != protocol.ErrorSessionTargetDenied {
		t.Fatalf("unowned browser session error = %#v, want %s", err, protocol.ErrorSessionTargetDenied)
	}
}
