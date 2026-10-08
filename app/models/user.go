package models

import "github.com/macrowallets/waas/pkg/authmodel"

// User is the auth user row. The struct lives in pkg/authmodel so config can
// register it without importing this package.
type User = authmodel.User
