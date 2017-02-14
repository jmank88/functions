package datastoreutil

import (
	"bytes"
	"strings"
)

// SplitPath splits path (without leading or trailing '/') on '/', and notes whether a trailing '/' was present.
func SplitPath(path string) (parts []string, trailingSlash bool) {
	if len(path) > 0 && path[0] == '/' {
		path = path[1:]
	}
	trailingSlash = len(path) > 0 && path[len(path)-1] == '/'
	if trailingSlash {
		path = path[:len(path)-1]
	}
	if len(path) > 0 {
		parts = strings.Split(path, "/")
	}
	return
}

// SqlLikeToRegExp converts an sql 'LIKE' query into an equivalent regular expression.
func SqlLikeToRegExp(l string) string {
	return "^" + strings.Replace(l, "%", ".*?", -1) + "$"
}

// StripParamNames removes parameter names from a route, leaving only wildcards.
// Example: `/blogs/:blog_id/comments/:comment_id` -> `/blogs/:/comments/:`
func StripParamNames(route string) string {
	var b bytes.Buffer

	i := 0
	for i >= 0 {
		route = route[i:]
		j := strings.IndexAny(route, ":*")
		if j < 0 {
			b.WriteString(route)
			break
		}
		b.WriteString(route[:j+1])
		route = route[j+1:]
		i = strings.IndexRune(route, '/')
	}

	return b.String()
}