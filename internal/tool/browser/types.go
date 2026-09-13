package browser

import (
	"context"
	"sync"
	"time"

	"github.com/chromedp/cdproto/target"
)

type Kind string

const (
	BrowserAuto     Kind = "auto"
	BrowserChrome   Kind = "chrome"
	BrowserChromium Kind = "chromium"
	BrowserEdge     Kind = "edge"
)

type WaitUntil string

const (
	WaitDOMContentLoaded WaitUntil = "domcontentloaded"
	WaitLoad             WaitUntil = "load"
)

type ElementState string

const (
	StateVisible  ElementState = "visible"
	StateHidden   ElementState = "hidden"
	StateAttached ElementState = "attached"
	StateDetached ElementState = "detached"
)

type Viewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type Size struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	URL      string  `json:"url,omitempty"`
	Domain   string  `json:"domain,omitempty"`
	Path     string  `json:"path,omitempty"`
	Expires  float64 `json:"expires,omitempty"`
	HTTPOnly bool    `json:"http_only,omitempty"`
	Secure   bool    `json:"secure,omitempty"`
	SameSite string  `json:"same_site,omitempty"`
}

type StartRequest struct {
	URL                     string
	Browser                 Kind
	Headless                bool
	Viewport                Viewport
	ProfileID               string
	CDPURL                  string
	Cookies                 []Cookie
	LocalStorage            map[string]map[string]string
	ReloadAfterLocalStorage bool
	Timeout                 time.Duration
}

type CloseRequest struct {
	SessionID string
}

type CleanupRequest struct {
	MaxAge time.Duration
}

type ActRequest struct {
	SessionID              string
	PageID                 string
	Actions                []Action
	CloseAfter             bool
	FullPage               bool
	MaxTextChars           int
	MaxInteractiveElements int
	Timeout                time.Duration
}

type SnapshotRequest struct {
	SessionID              string
	PageID                 string
	CloseAfter             bool
	FullPage               bool
	MaxTextChars           int
	MaxInteractiveElements int
	Timeout                time.Duration
}

type Action struct {
	Kind         string
	Goto         *GotoAction
	Click        *ClickAction
	Fill         *FillAction
	Press        *PressAction
	Wait         *WaitAction
	WaitSelector *WaitSelectorAction
	WaitURL      *WaitURLAction
	WaitText     *WaitTextAction
	WaitResponse *WaitResponseAction
	Select       *SelectAction
	Scroll       *ScrollAction
	Navigation   *NavigationAction
}

type GotoAction struct {
	URL       string
	WaitUntil WaitUntil
	Timeout   time.Duration
}

type ClickAction struct{ Selector string }

type FillAction struct {
	Selector string
	Value    string
}

type PressAction struct {
	Selector string
	Key      string
}

type WaitAction struct{ Duration time.Duration }

type WaitSelectorAction struct {
	Selector string
	State    ElementState
	Timeout  time.Duration
}

type WaitURLAction struct {
	URL     string
	Timeout time.Duration
}

type WaitTextAction struct {
	Text    string
	Exact   bool
	State   ElementState
	Timeout time.Duration
}

type WaitResponseAction struct {
	URL        string
	URLPattern string
	Method     string
	Status     int
	Timeout    time.Duration
}

type SelectAction struct {
	Selector string
	Value    string
}

type ScrollAction struct {
	DeltaX int64
	DeltaY int64
}

type NavigationAction struct {
	WaitUntil WaitUntil
	Timeout   time.Duration
}

type PageSummary struct {
	PageID string `json:"page_id"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	Active bool   `json:"active"`
}

type FocusedElement struct {
	Tag        string `json:"tag"`
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	Type       string `json:"type,omitempty"`
	Text       string `json:"text,omitempty"`
	Value      string `json:"value,omitempty"`
	ARIAName   string `json:"aria_name,omitempty"`
	Selector   string `json:"selector,omitempty"`
	IsEditable bool   `json:"is_editable,omitempty"`
}

type InteractiveElement struct {
	Tag      string `json:"tag"`
	Type     string `json:"type,omitempty"`
	Text     string `json:"text,omitempty"`
	ARIAName string `json:"aria_name,omitempty"`
	Href     string `json:"href,omitempty"`
	Selector string `json:"selector,omitempty"`
}

type ConsoleError struct {
	Message string `json:"message"`
}

type NetworkError struct {
	URL       string `json:"url,omitempty"`
	Method    string `json:"method,omitempty"`
	ErrorText string `json:"error_text"`
}

type PageError struct {
	Message string `json:"message"`
}

type Snapshot struct {
	SessionID           string               `json:"session_id"`
	PageID              string               `json:"page_id"`
	Pages               []PageSummary        `json:"pages"`
	URL                 string               `json:"url"`
	Title               string               `json:"title"`
	Text                string               `json:"text"`
	Viewport            Viewport             `json:"viewport"`
	PageSize            Size                 `json:"page_size"`
	FocusedElement      *FocusedElement      `json:"focused_element,omitempty"`
	InteractiveElements []InteractiveElement `json:"interactive_elements"`
	ConsoleErrors       []ConsoleError       `json:"console_errors"`
	NetworkErrors       []NetworkError       `json:"network_errors"`
	PageErrors          []PageError          `json:"page_errors"`
	PNG                 []byte               `json:"-"`
}

type StartResult struct {
	SessionID      string        `json:"session_id"`
	PageID         string        `json:"page_id"`
	Pages          []PageSummary `json:"pages"`
	URL            string        `json:"url"`
	Title          string        `json:"title"`
	ProfileID      string        `json:"profile_id,omitempty"`
	ConnectionMode string        `json:"connection_mode"`
}

type CloseResult struct {
	SessionID string `json:"session_id"`
	Closed    bool   `json:"closed"`
}

type CleanupResult struct {
	RemovedCount    int      `json:"removed_count"`
	RemovedSessions []string `json:"removed_sessions"`
}

type pageState struct {
	ID    target.ID
	URL   string
	Title string
	Order uint64
}

type pageContext struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type session struct {
	opMu sync.Mutex
	mu   sync.Mutex

	id               string
	kind             Kind
	profileID        string
	profileDir       string
	temporaryProfile bool
	external         bool
	projectOwner     projectOwner
	ownedTargets     map[target.ID]struct{}
	createdAt        time.Time
	lastActivity     time.Time

	allocatorCtx    context.Context
	allocatorCancel context.CancelFunc
	browserCtx      context.Context
	browserCancel   context.CancelFunc

	pages        map[target.ID]*pageState
	pageContexts map[target.ID]*pageContext
	activePage   target.ID
	pageOrder    uint64
	closed       bool
}
