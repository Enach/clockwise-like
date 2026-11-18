// The sfguard self-test fixture. It borrows the backend's module path so the
// guard's targets (backend/storage, backend/auth, ...) resolve exactly as in
// the real module. Under testdata/, so no go command in the repo builds it.
module github.com/Enach/paceday/backend

go 1.26.0
