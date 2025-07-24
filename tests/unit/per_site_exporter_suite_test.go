package unit

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPerSiteExporter(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Per-Site Exporter Unit Tests")
}
