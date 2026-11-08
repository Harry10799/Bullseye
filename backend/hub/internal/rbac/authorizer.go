// Package rbac implements namespace-scoped authorization using K8s
// SubjectAccessReview. When OIDC is enabled, the hub validates the user's
// token against the cluster's RBAC so they can only see namespaces they
// have access to.
//
// When OIDC is disabled (dev mode), all requests are treated as cluster-admin.
package rbac

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Authorizer checks whether a user can access a given namespace.
type Authorizer struct {
	client    kubernetes.Interface
	enabled   bool

	// cache: "user|namespace|verb" -> (allowed, expiry)
	mu    sync.RWMutex
	cache map[string]cacheEntry
	ttl   time.Duration
}

type cacheEntry struct {
	allowed bool
	expires time.Time
}

// New creates an Authorizer. If enabled=false, all access is granted
// (dev mode).
func New(client kubernetes.Interface, enabled bool) *Authorizer {
	return &Authorizer{
		client:  client,
		enabled: enabled,
		cache:   make(map[string]cacheEntry),
		ttl:     30 * time.Second, // K8s RBAC changes are rare; short cache is fine
	}
}

// CanViewNamespace checks whether user can list/get pods in the namespace.
// This is the permission Bullseye needs to show that namespace's traffic.
func (a *Authorizer) CanViewNamespace(ctx context.Context, user, namespace string) (bool, error) {
	if !a.enabled {
		return true, nil
	}
	if namespace == "" || namespace == "__all__" {
		// "__all__" — must be able to view ALL namespaces; check the empty namespace
		return a.checkSAR(ctx, user, "", "list", "pods")
	}
	return a.checkSAR(ctx, user, namespace, "list", "pods")
}

// VisibleNamespaces returns the list of namespaces the user can view.
// This is computed by listing all namespaces and checking each one. For
// large clusters, prefer calling CanViewNamespace per-namespace from the UI.
func (a *Authorizer) VisibleNamespaces(ctx context.Context, user string) ([]string, error) {
	if !a.enabled {
		return nil, nil // nil means "all"
	}
	nsList, err := a.client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("rbac: list namespaces: %w", err)
	}
	out := make([]string, 0, len(nsList.Items))
	for _, ns := range nsList.Items {
		ok, err := a.CanViewNamespace(ctx, user, ns.Name)
		if err != nil {
			log.Warn().Err(err).Str("ns", ns.Name).Msg("rbac: check failed")
			continue
		}
		if ok {
			out = append(out, ns.Name)
		}
	}
	return out, nil
}

func (a *Authorizer) checkSAR(ctx context.Context, user, namespace, verb, resource string) (bool, error) {
	cacheKey := strings.Join([]string{user, namespace, verb, resource}, "|")

	a.mu.RLock()
	if e, ok := a.cache[cacheKey]; ok && time.Now().Before(e.expires) {
		a.mu.RUnlock()
		return e.allowed, nil
	}
	a.mu.RUnlock()

	sar := &authv1.SubjectAccessReview{
		Spec: authv1.SubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      verb,
				Resource:  resource,
				Group:     "",
			},
			User: user,
		},
	}

	sar, err := a.client.AuthorizationV1().SubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("rbac: SAR create: %w", err)
	}

	a.mu.Lock()
	a.cache[cacheKey] = cacheEntry{
		allowed: sar.Status.Allowed,
		expires: time.Now().Add(a.ttl),
	}
	a.mu.Unlock()

	return sar.Status.Allowed, nil
}

// InvalidateCache clears all cached decisions. Call this when the user's
// groups/roles may have changed (e.g., on token refresh).
func (a *Authorizer) InvalidateCache() {
	a.mu.Lock()
	a.cache = make(map[string]cacheEntry)
	a.mu.Unlock()
}
