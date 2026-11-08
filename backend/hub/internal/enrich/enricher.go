// Package enrich correlates raw IP:port endpoints with Kubernetes pod/service
// metadata using shared informers from client-go.
//
// The enricher maintains an in-memory index of every Pod and Service in the
// cluster, keyed by IP address. When a CaptureEntry arrives from a sniffer,
// the hub calls EnrichEntry() which fills in the source/destination pod,
// service, and namespace fields before persisting and relaying to the UI.
package enrich

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// Enricher maintains a live IP → Pod/Service index backed by K8s informers.
type Enricher struct {
	client kubernetes.Interface

	// ip -> PodInfo
	muPod   sync.RWMutex
	podByIP map[string]*PodInfo
	// ip -> ServiceInfo (cluster IPs)
	muSvc   sync.RWMutex
	svcByIP map[string]*ServiceInfo

	// reverse lookup: pod name -> service names that select it
	muSelector sync.RWMutex
	podToSvc   map[string][]string

	factory informers.SharedInformerFactory
}

// PodInfo is the subset of pod metadata Bullseye needs (memory-cheap).
type PodInfo struct {
	Name      string
	Namespace string
	IP        string
	HostIP    string
	NodeName  string
	Labels    map[string]string
	Phase     corev1.PodPhase
}

// ServiceInfo is the subset of service metadata Bullseye needs.
type ServiceInfo struct {
	Name      string
	Namespace string
	ClusterIP string
	Type      corev1.ServiceType
	Ports     []corev1.ServicePort
	Selector  map[string]string
}

// New creates an Enricher and starts watching K8s for pod/service changes.
// Resync interval controls how often the full cache is re-listed (safety net
// for missed watch events). 10 minutes is the kubeshark default.
func New(ctx context.Context, client kubernetes.Interface, resync time.Duration) (*Enricher, error) {
	e := &Enricher{
		client:    client,
		podByIP:   make(map[string]*PodInfo),
		svcByIP:   make(map[string]*ServiceInfo),
		podToSvc:  make(map[string][]string),
		factory:   informers.NewSharedInformerFactory(client, resync),
	}

	podInformer := e.factory.Core().V1().Pods().Informer()
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    e.onPodAdd,
		UpdateFunc: e.onPodUpdate,
		DeleteFunc: e.onPodDelete,
	})

	svcInformer := e.factory.Core().V1().Services().Informer()
	svcInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    e.onSvcAdd,
		UpdateFunc: e.onSvcUpdate,
		DeleteFunc: e.onSvcDelete,
	})

	e.factory.Start(ctx.Done())

	// wait for cache sync so we have a populated index before serving traffic
	log.Info().Msg("enricher: waiting for cache sync")
	if !cache.WaitForCacheSync(ctx.Done(), podInformer.HasSynced, svcInformer.HasSynced) {
		return nil, fmt.Errorf("enricher: cache sync failed/timed out")
	}
	log.Info().Msg("enricher: cache synced")

	// rebuild selector index now that we have all pods + services
	e.rebuildSelectorIndex()

	return e, nil
}

// LookupPod returns pod metadata for the given IP, or nil if not found.
// Lookup order: pod IP → host IP (for hostNetwork pods) → service cluster IP.
func (e *Enricher) LookupPod(ip string) *PodInfo {
	e.muPod.RLock()
	defer e.muPod.RUnlock()
	return e.podByIP[ip]
}

// LookupService returns service metadata for the given cluster IP, or nil.
func (e *Enricher) LookupService(ip string) *ServiceInfo {
	e.muSvc.RLock()
	defer e.muSvc.RUnlock()
	return e.svcByIP[ip]
}

// LookupServiceForPod returns the services that select the given pod.
// A pod can be selected by multiple services (e.g., headless + clusterIP).
func (e *Enricher) LookupServiceForPod(podName, namespace string) []string {
	key := namespace + "/" + podName
	e.muSelector.RLock()
	defer e.muSelector.RUnlock()
	return e.podToSvc[key]
}

// --- informer event handlers ---

func (e *Enricher) onPodAdd(obj interface{}) {
	pod := obj.(*corev1.Pod)
	e.indexPod(pod)
}

func (e *Enricher) onPodUpdate(old, cur interface{}) {
	oldPod := old.(*corev1.Pod)
	curPod := cur.(*corev1.Pod)
	// remove old IPs that may have changed
	e.unindexPod(oldPod)
	e.indexPod(curPod)
}

func (e *Enricher) onPodDelete(obj interface{}) {
	pod := obj.(*corev1.Pod)
	e.unindexPod(pod)
}

func (e *Enricher) onSvcAdd(obj interface{}) {
	svc := obj.(*corev1.Service)
	e.indexService(svc)
	e.rebuildSelectorIndex() // selectors may now match different pods
}

func (e *Enricher) onSvcUpdate(old, cur interface{}) {
	oldSvc := old.(*corev1.Service)
	curSvc := cur.(*corev1.Service)
	e.unindexService(oldSvc)
	e.indexService(curSvc)
	e.rebuildSelectorIndex()
}

func (e *Enricher) onSvcDelete(obj interface{}) {
	svc := obj.(*corev1.Service)
	e.unindexService(svc)
	e.rebuildSelectorIndex()
}

func (e *Enricher) indexPod(pod *corev1.Pod) {
	info := &PodInfo{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		IP:        pod.Status.PodIP,
		HostIP:    pod.Status.HostIP,
		NodeName:  pod.Spec.NodeName,
		Labels:    pod.Labels,
		Phase:     pod.Status.Phase,
	}
	if info.IP == "" || info.IP == "0.0.0.0" {
		return
	}
	e.muPod.Lock()
	e.podByIP[info.IP] = info
	// also index by host IP for hostNetwork pods
	if info.HostIP != "" && info.HostIP != info.IP {
		e.podByIP[info.HostIP] = info
	}
	e.muPod.Unlock()
}

func (e *Enricher) unindexPod(pod *corev1.Pod) {
	e.muPod.Lock()
	defer e.muPod.Unlock()
	if pod.Status.PodIP != "" {
		delete(e.podByIP, pod.Status.PodIP)
	}
	// do NOT delete host IP entry — multiple pods may share a node
}

func (e *Enricher) indexService(svc *corev1.Service) {
	if svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == "None" {
		return
	}
	info := &ServiceInfo{
		Name:      svc.Name,
		Namespace: svc.Namespace,
		ClusterIP: svc.Spec.ClusterIP,
		Type:      svc.Spec.Type,
		Ports:     svc.Spec.Ports,
		Selector:  svc.Spec.Selector,
	}
	e.muSvc.Lock()
	e.svcByIP[info.ClusterIP] = info
	// also index external IPs for LoadBalancer services
	for _, ext := range svc.Spec.ExternalIPs {
		e.svcByIP[ext] = info
	}
	for _, ing := range svc.Status.LoadBalancer.Ingress {
		if ing.IP != "" {
			e.svcByIP[ing.IP] = info
		}
	}
	e.muSvc.Unlock()
}

func (e *Enricher) unindexService(svc *corev1.Service) {
	e.muSvc.Lock()
	defer e.muSvc.Unlock()
	if svc.Spec.ClusterIP != "" && svc.Spec.ClusterIP != "None" {
		delete(e.svcByIP, svc.Spec.ClusterIP)
	}
}

// rebuildSelectorIndex rebuilds the pod → service mapping by iterating all
// services and matching their selectors against all pods. Called on service
// add/update/delete. Pods usually change more frequently than services, so
// we rebuild on service events; pod events use the existing index.
func (e *Enricher) rebuildSelectorIndex() {
	e.muSvc.RLock()
	allSvcs := make([]*ServiceInfo, 0, len(e.svcByIP))
	seen := make(map[string]bool)
	for _, svc := range e.svcByIP {
		key := svc.Namespace + "/" + svc.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(svc.Selector) > 0 {
			allSvcs = append(allSvcs, svc)
		}
	}
	e.muSvc.RUnlock()

	// list all pods via the informer cache (no API call)
	podLister := e.factory.Core().V1().Pods().Lister()
	allPods, err := podLister.List(labels.Everything())
	if err != nil {
		log.Warn().Err(err).Msg("enricher: failed to list pods for selector rebuild")
		return
	}

	newIdx := make(map[string][]string)
	for _, pod := range allPods {
		key := pod.Namespace + "/" + pod.Name
		for _, svc := range allSvcs {
			if svc.Namespace != pod.Namespace {
				continue
			}
			if labels.SelectorFromSet(svc.Selector).Matches(labels.Set(pod.Labels)) {
				newIdx[key] = append(newIdx[key], svc.Name)
			}
		}
	}

	e.muSelector.Lock()
	e.podToSvc = newIdx
	e.muSelector.Unlock()
}

// Stats returns counts for observability.
func (e *Enricher) Stats() (pods, services int) {
	e.muPod.RLock()
	pods = len(e.podByIP)
	e.muPod.RUnlock()
	e.muSvc.RLock()
	services = len(e.svcByIP)
	e.muSvc.RUnlock()
	return
}

// ListServices returns all known services — used to emit cluster:topology
// to the UI on connect.
func (e *Enricher) ListServices() []ServiceInfo {
	e.muSvc.RLock()
	defer e.muSvc.RUnlock()
	seen := make(map[string]bool)
	out := make([]ServiceInfo, 0, len(e.svcByIP))
	for _, svc := range e.svcByIP {
		key := svc.Namespace + "/" + svc.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, *svc)
	}
	return out
}

// ListPodsByService returns all pods selected by a given service name+ns.
func (e *Enricher) ListPodsByService(svcName, namespace string) []*PodInfo {
	svc, err := e.factory.Core().V1().Services().Lister().Services(namespace).Get(svcName)
	if err != nil {
		return nil
	}
	if len(svc.Spec.Selector) == 0 {
		return nil
	}
	pods, err := e.factory.Core().V1().Pods().Lister().Pods(namespace).List(labels.SelectorFromSet(svc.Spec.Selector))
	if err != nil {
		return nil
	}
	out := make([]*PodInfo, 0, len(pods))
	for _, p := range pods {
		out = append(out, &PodInfo{
			Name:      p.Name,
			Namespace: p.Namespace,
			IP:        p.Status.PodIP,
			HostIP:    p.Status.HostIP,
			NodeName:  p.Spec.NodeName,
			Labels:    p.Labels,
			Phase:     p.Status.Phase,
		})
	}
	return out
}

// Suppress unused-import warnings for metav1 (used in other files of package).
var _ = metav1.ObjectMeta{}
