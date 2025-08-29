package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPerSiteExporterUnit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Per-Site Exporter Unit Suite (package main)")
}
