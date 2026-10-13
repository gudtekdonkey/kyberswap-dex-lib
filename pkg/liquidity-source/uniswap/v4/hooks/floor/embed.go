package floor

import _ "embed"

//go:embed abis/SingletonFloorHook.json
var hookABIJson []byte

//go:embed abis/FloorPoolViews.json
var poolViewsABIJson []byte

//go:embed abis/StateView.json
var stateViewABIJson []byte
