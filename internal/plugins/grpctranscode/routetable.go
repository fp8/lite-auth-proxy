package grpctranscode

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// routeEntry maps an HTTP verb+path to a gRPC method.
type routeEntry struct {
	httpMethod string // GET, POST, PUT, DELETE, PATCH
	pathRegex  *regexp.Regexp
	pathTpl    string // original template for logging
	varNames   []string
	grpcMethod string // e.g. "/pkg.Service/Method"
	inputDesc  protoreflect.MessageDescriptor
	outputDesc protoreflect.MessageDescriptor
	bodyField  string // "*" = entire body, "" = no body, else field name
	backend    *backendConn
}

// routeTable holds all discovered routes and supports concurrent reads while
// individual backends are (re)discovered. Routes are kept per backend so each
// backend's discovery can update its own routes independently — backends become
// ready at different times (sidecar cold start), and refresh re-discovers them
// on their own schedule.
type routeTable struct {
	mu        sync.RWMutex
	byBackend map[*backendConn][]routeEntry
	flat      []routeEntry // rebuilt from byBackend on every change
}

func newRouteTable() *routeTable {
	return &routeTable{byBackend: map[*backendConn][]routeEntry{}}
}

// match finds the first route matching the given HTTP method and path.
// Returns the matched entry and a map of path variable bindings, or nil.
func (rt *routeTable) match(method, path string) (*routeEntry, map[string]string) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	for i := range rt.flat {
		e := &rt.flat[i]
		if e.httpMethod != method {
			continue
		}
		matches := e.pathRegex.FindStringSubmatch(path)
		if matches == nil {
			continue
		}
		vars := make(map[string]string, len(e.varNames))
		for j, name := range e.varNames {
			vars[name] = matches[j+1]
		}
		return e, vars
	}
	return nil, nil
}

// setBackendRoutes atomically replaces the routes contributed by one backend
// and rebuilds the flattened lookup slice.
func (rt *routeTable) setBackendRoutes(bc *backendConn, entries []routeEntry) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.byBackend[bc] = entries
	var flat []routeEntry
	for _, es := range rt.byBackend {
		flat = append(flat, es...)
	}
	rt.flat = flat
}

// routeCount returns the number of registered routes.
func (rt *routeTable) routeCount() int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return len(rt.flat)
}

// buildConventionRoute creates a POST route for convention mode:
// POST /[baseURL/]<package>.<Service>/<Method>
func buildConventionRoute(
	baseURL string,
	fullMethod string, // e.g. "pkg.Service/Method"
	inputDesc, outputDesc protoreflect.MessageDescriptor,
	backend *backendConn,
) routeEntry {
	path := "/" + fullMethod
	if baseURL != "" {
		path = "/" + strings.Trim(baseURL, "/") + path
	}
	return routeEntry{
		httpMethod: "POST",
		pathRegex:  regexp.MustCompile("^" + regexp.QuoteMeta(path) + "$"),
		pathTpl:    path,
		grpcMethod: "/" + fullMethod,
		inputDesc:  inputDesc,
		outputDesc: outputDesc,
		bodyField:  "*",
		backend:    backend,
	}
}

// buildAnnotationRoute creates a route from a google.api.http annotation.
func buildAnnotationRoute(
	baseURL string,
	httpMethod string,
	pathTemplate string, // e.g. "/v1/messages/{name}"
	bodyField string,
	fullMethod string,
	inputDesc, outputDesc protoreflect.MessageDescriptor,
	backend *backendConn,
) (routeEntry, error) {
	if baseURL != "" {
		pathTemplate = "/" + strings.Trim(baseURL, "/") + pathTemplate
	}

	pattern, varNames, err := compilePathTemplate(pathTemplate)
	if err != nil {
		return routeEntry{}, fmt.Errorf("invalid path template %q: %w", pathTemplate, err)
	}

	return routeEntry{
		httpMethod: httpMethod,
		pathRegex:  pattern,
		pathTpl:    pathTemplate,
		varNames:   varNames,
		grpcMethod: "/" + fullMethod,
		inputDesc:  inputDesc,
		outputDesc: outputDesc,
		bodyField:  bodyField,
		backend:    backend,
	}, nil
}

// compilePathTemplate converts a google.api.http path template like
// "/v1/messages/{name}" into a regex and extracts variable names.
// Supports {var} and {var=*} and {var=**} patterns.
func compilePathTemplate(tpl string) (*regexp.Regexp, []string, error) {
	var varNames []string
	// Replace {var}, {var=*}, {var=**} with named capture groups.
	re := regexp.MustCompile(`\{([^}=]+)(?:=[^}]*)?\}`)
	pattern := re.ReplaceAllStringFunc(tpl, func(match string) string {
		sub := re.FindStringSubmatch(match)
		name := sub[1]
		varNames = append(varNames, name)
		if strings.Contains(match, "=**") {
			return "(.+)"
		}
		return "([^/]+)"
	})
	compiled, err := regexp.Compile("^" + pattern + "$")
	if err != nil {
		return nil, nil, err
	}
	return compiled, varNames, nil
}
