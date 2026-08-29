//go:build !gcp

package main

import (
	"fmt"

	"ratw/internal/store"
)

func newCloudStore(string) (store.Store, error) {
	return nil, fmt.Errorf("this binary was built without -tags gcp; set RATW_LOCAL=true")
}
