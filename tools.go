//go:build tools
// +build tools

// This file declares dependencies on tools used in the build process.
// See: https://github.com/go-modules-by-example/index/blob/master/010_tools/README.md

package tools

import (
	_ "github.com/boynux/squid-exporter"
	_ "github.com/onsi/ginkgo/v2/ginkgo"
	_ "helm.sh/helm/v3/cmd/helm"
	// Test dependencies - imported here so Cachi2 will prefetch them
	_ "github.com/stretchr/testify/assert"
	_ "gopkg.in/check.v1"
	_ "github.com/pmezard/go-difflib/difflib"
	_ "github.com/prashantv/gostub"
)

