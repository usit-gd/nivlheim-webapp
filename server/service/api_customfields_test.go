package main

import (
	"net/http"
	"os"
	"testing"
)

func TestApiMethodCustomFields(t *testing.T) {
	if os.Getenv("NOPOSTGRES") != "" {
		t.Log("No Postgres, skipping test")
		return
	}
	tests := []apiCall{
		// At first, an empty list
		{
			methodAndPath: "GET /api/v2/settings/customfields?fields=name",
			expectStatus:  http.StatusOK,
			expectJSON:    "[]",
		},
		// Create one item
		{
			methodAndPath: "POST /api/v2/settings/customfields",
			body:          "name=siteadmin&filename=%2Fetc%2Fsiteadmin&regexp=%2E%2B",
			expectStatus:  http.StatusCreated,
		},
		// Now, the list contains that item
		{
			methodAndPath: "GET /api/v2/settings/customfields?fields=name",
			expectStatus:  http.StatusOK,
			expectJSON:    "[{\"name\":\"siteadmin\"}]",
		},
		// Read the item details
		{
			methodAndPath: "GET /api/v2/settings/customfields/siteadmin?fields=name,filename,regexp",
			expectStatus:  http.StatusOK,
			expectJSON:    "{\"name\":\"siteadmin\",\"filename\":\"/etc/siteadmin\",\"regexp\":\".+\"}",
		},
		// Change the name of the item
		{
			methodAndPath: "PUT /api/v2/settings/customfields/siteadmin",
			body:          "name=siteowner&filename=/etc/bob&regexp=%2E%2B",
			expectStatus:  http.StatusNoContent,
		},
		// The list verifies the name is changed
		{
			methodAndPath: "GET /api/v2/settings/customfields?fields=name",
			expectStatus:  http.StatusOK,
			expectJSON:    "[{\"name\":\"siteowner\"}]",
		},
		// The item details are correct with the new name
		{
			methodAndPath: "GET /api/v2/settings/customfields/siteowner?fields=name,filename,regexp",
			expectStatus:  http.StatusOK,
			expectJSON:    "{\"name\":\"siteowner\",\"filename\":\"/etc/bob\",\"regexp\":\".+\"}",
		},
		// Delete the item
		{
			methodAndPath: "DELETE /api/v2/settings/customfields/siteowner",
			expectStatus:  http.StatusNoContent,
		},
		// The list should be empty now
		{
			methodAndPath: "GET /api/v2/settings/customfields?fields=name",
			expectStatus:  http.StatusOK,
			expectJSON:    "[]",
		},
		// Trying to overwrite an item that doesn't exist should give an error
		{
			methodAndPath: "PUT /api/v2/settings/customfields/unicorn",
			body:          "filename=/etc/foo&regexp=(.*)",
			expectStatus:  http.StatusNotFound,
		},
		// Try to create an item, but use the wrong case for field names. Should still work
		{
			methodAndPath: "POST /api/v2/settings/customfields",
			body:          "NaMe=foo&FiLeNaMe=%2Fetc%2Fblah&regExp=baz",
			expectStatus:  http.StatusCreated,
		},
		// Same as above, but update
		{
			methodAndPath: "PUT /api/v2/settings/customfields/foo",
			body:          "NaMe=bar&FiLeNaMe=%2Fetc%2Fblah&regExp=baz",
			expectStatus:  http.StatusNoContent,
		},
	}

	db := getDBconnForTesting(t)
	defer db.Close()

	mux := http.NewServeMux()
	mux.Handle("/api/v2/settings/customfields",
		wrapRequireAuth(&apiMethodCustomFieldsCollection{db: db}, db))
	mux.Handle("/api/v2/settings/customfields/",
		wrapRequireAuth(&apiMethodCustomFieldsItem{db: db}, db))

	testAPIcalls(t, mux, tests)
}
