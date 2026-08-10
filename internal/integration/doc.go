// Package integration checks that the pieces agree on one realistic
// configuration: what the sources decode, and what the generated schema
// accepts, are the same document.
//
// It lives outside the core so it may import a source and a target at once,
// which the layering forbids everywhere else.
package integration
