package middleware

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
)

func MustOrgID(c *gin.Context) string {
	rawOrgID, ok := c.Get("org_id")
	if !ok {
		panic("org_id missing from request context")
	}

	orgID, ok := rawOrgID.(string)
	if !ok {
		panic(fmt.Sprintf("org_id has invalid type %T", rawOrgID))
	}

	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		panic("org_id is empty in request context")
	}

	return orgID
}
