package server

import "github.com/valyala/fasthttp"

type route struct {
	method  string
	path    string
	handler fasthttp.RequestHandler
}

type Router struct {
	routes []route
}

func NewRouter() *Router {
	return &Router{}
}

func (r *Router) GET(path string, handler fasthttp.RequestHandler) {
	r.routes = append(r.routes, route{"GET", path, handler})
}

func (r *Router) POST(path string, handler fasthttp.RequestHandler) {
	r.routes = append(r.routes, route{"POST", path, handler})
}

func (r *Router) PUT(path string, handler fasthttp.RequestHandler) {
	r.routes = append(r.routes, route{"PUT", path, handler})
}

func (r *Router) DELETE(path string, handler fasthttp.RequestHandler) {
	r.routes = append(r.routes, route{"DELETE", path, handler})
}

// Lookup returns the matching handler and extracted path parameters.
func (r *Router) Lookup(method, path string) (fasthttp.RequestHandler, map[string]string) {
	for _, rt := range r.routes {
		if rt.method != method {
			continue
		}
		if params, ok := matchPath(rt.path, path); ok {
			return rt.handler, params
		}
	}
	return nil, nil
}

// matchPath supports exact match, wildcard suffix (*), and :param placeholders.
func matchPath(pattern, path string) (map[string]string, bool) {
	if pattern == path {
		return nil, true
	}
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			return nil, true
		}
	}
	params := make(map[string]string)
	pi, pa := 0, 0
	for pi < len(pattern) && pa < len(path) {
		if pattern[pi] == ':' {
			j := pi + 1
			for j < len(pattern) && pattern[j] != '/' {
				j++
			}
			paramName := pattern[pi+1 : j]
			k := pa
			for k < len(path) && path[k] != '/' {
				k++
			}
			params[paramName] = path[pa:k]
			pi = j
			pa = k
		} else if pattern[pi] != path[pa] {
			return nil, false
		} else {
			pi++
			pa++
		}
	}
	if pi == len(pattern) && pa == len(path) {
		return params, true
	}
	return nil, false
}
