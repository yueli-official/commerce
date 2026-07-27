package runtime

import (
	"os"

	"github.com/gogf/gf/v2/net/ghttp"
	foundationopenapi "github.com/yueli-official/foundation/go/goframe/openapi"
)

const openAPIOutputEnv = "PLATFORM_OPENAPI_OUTPUT"

func OpenAPIRequested() bool {
	return os.Getenv(openAPIOutputEnv) != ""
}

func ExportOpenAPIIfRequested(server *ghttp.Server) (handled bool, err error) {
	output := os.Getenv(openAPIOutputEnv)
	if output == "" {
		return false, nil
	}
	return true, foundationopenapi.Export(foundationopenapi.ExportConfig{
		Server: server, Output: output, Overwrite: true,
	})
}
