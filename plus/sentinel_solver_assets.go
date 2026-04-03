package plus

import _ "embed"

//go:embed js/sentinel_sdk.js
var sentinelSDKScript string

//go:embed sentinel_solver_runner.js
var sentinelSolverRunner string
