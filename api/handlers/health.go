package handlers

import "github.com/gin-gonic/gin"

type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

func Health(version string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(200, HealthResponse{
			Status:  "ok",
			Version: version,
		})
	}
}
