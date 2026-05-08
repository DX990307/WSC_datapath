package sampledrunner

import (
	"flag"
	"fmt"
)

var PhotonDebugFlag = flag.Bool("photon-debug", false,
	"print Photon sampled-execution debug events.")
var PhotonDebugVerboseFlag = flag.Bool("photon-debug-verbose", false,
	"print every Photon sampled-execution debug decision.")

func PhotonDebugf(label, format string, args ...interface{}) {
	if !*PhotonDebugFlag && !*PhotonDebugVerboseFlag {
		return
	}
	printPhotonDebug(label, format, args...)
}

func PhotonVerbosef(label, format string, args ...interface{}) {
	if !*PhotonDebugVerboseFlag {
		return
	}
	printPhotonDebug(label, format, args...)
}

func printPhotonDebug(label, format string, args ...interface{}) {
	if label == "" {
		label = "global"
	}
	fmt.Printf("[Photon][%s] %s\n", label, fmt.Sprintf(format, args...))
}
