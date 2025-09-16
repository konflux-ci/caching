package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSquidStoreIdUnit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Squid Store-ID Unit Suite (package main)")
}
