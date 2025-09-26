package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestICAPServerUnit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ICAP Server Unit Suite (package main)")
}
