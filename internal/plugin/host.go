package plugin

import (
	"api-manager/internal/store"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"io"
	"sync"
)

const hostOutputBytes = 2 << 20
const hostInputBytes = 384 << 10

// guestHostBudget limits side effects independently of guest CPU/memory limits.
type guestHostBudget struct {
	mu    sync.Mutex
	calls int
}
type hostBudgetKey struct{}
type hostIdentityKey struct{}
type hostRequest struct {
	Operation   string              `json:"operation"`
	HTTPRequest *hostHTTPRequest    `json:"http,omitempty"`
	Session     *hostSessionRequest `json:"session,omitempty"`
}
type hostResponse struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Result any    `json:"result,omitempty"`
}

func (r *Registry) instantiateHost(ctx context.Context, runtime wazero.Runtime, manifest Manifest) error {
	r.mu.RLock()
	services := r.services
	r.mu.RUnlock()
	identity := services.snapshot(manifest.Name).ID
	_, e := runtime.NewHostModuleBuilder("api_manager").NewFunctionBuilder().WithFunc(func(ctx context.Context, module api.Module, requestPtr, requestLen, responsePtr, responseCap uint32) uint64 {
		// Validate all guest ranges and output capacity BEFORE any side effects. The
		// caller cannot cause a repeated HTTP POST by probing for a response length.
		if requestLen == 0 || requestLen > hostInputBytes || responseCap < hostOutputBytes {
			return uint64(1)
		}
		input, ok := module.Memory().Read(requestPtr, requestLen)
		if !ok {
			return uint64(1)
		}
		if _, ok = module.Memory().Read(responsePtr, hostOutputBytes); !ok {
			return uint64(1)
		}
		raw := append([]byte(nil), input...)
		response := hostResponse{}
		budget, _ := ctx.Value(hostBudgetKey{}).(*guestHostBudget)
		if budget == nil {
			response.Error = "host calls not permitted during initialization"
		} else {
			budget.mu.Lock()
			allowed := budget.calls < 32
			budget.calls++
			budget.mu.Unlock()
			if !allowed {
				response.Error = "host call limit reached"
			} else {
				ctx = context.WithValue(ctx, hostIdentityKey{}, identity)
				result, e := r.dispatchHost(ctx, manifest, raw)
				if e != nil {
					if errors.Is(e, store.ErrConflict) {
						response.Error = "session or policy version changed"
					} else {
						response.Error = e.Error()
					}
				} else {
					response.OK = true
					response.Result = result
				}
			}
		}
		encoded, e := json.Marshal(response)
		if e != nil || len(encoded) > hostOutputBytes {
			return uint64(2)
		}
		if !module.Memory().Write(responsePtr, encoded) {
			return uint64(1)
		}
		return uint64(len(encoded)) << 32
	}).Export("host_call").Instantiate(ctx)
	return e
}
func (r *Registry) dispatchHost(ctx context.Context, manifest Manifest, raw []byte) (any, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	var q hostRequest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&q) != nil || d.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("invalid host call")
	}
	r.mu.RLock()
	services := r.services
	r.mu.RUnlock()
	if services == nil {
		return nil, errors.New("plugin host services unavailable")
	}
	if identity, ok := ctx.Value(hostIdentityKey{}).(string); ok && (identity == "" || services.snapshot(manifest.Name).ID != identity) {
		return nil, errors.New("plugin runtime identity changed")
	}
	switch q.Operation {
	case "http_request":
		if !hasCapability(manifest, "network") || q.HTTPRequest == nil || q.Session != nil {
			return nil, errors.New("network capability not declared")
		}
		return services.httpRequest(ctx, manifest.Name, *q.HTTPRequest)
	case "session_get", "session_put", "session_delete":
		if !hasCapability(manifest, "session_storage") || q.Session == nil || q.HTTPRequest != nil {
			return nil, errors.New("session storage capability not declared")
		}
		return services.session(ctx, manifest.Name, q.Operation, *q.Session)
	default:
		return nil, errors.New("unsupported host operation")
	}
}
