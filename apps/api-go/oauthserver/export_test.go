package oauthserver

// Expose only the isolated database harness to the external-package production
// Policy tests. This symbol is not present in a production build.
var ForTestDatabases = forDatabases
