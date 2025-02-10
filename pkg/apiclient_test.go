package gorest_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAPICLient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "APIClient Test Suite")
}
