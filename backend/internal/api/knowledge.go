package api

import "github.com/gin-gonic/gin"

func (s *Server) registerKnowledge(admin *gin.RouterGroup) {
	admin.PUT("/questions/:id/knowledge", func(c *gin.Context) {
		var input struct {
			Version int      `json:"version"`
			Tags    []string `json:"tags"`
		}
		if !bindCompetition(c, &input) {
			return
		}
		version, err := s.competitions().UpdateLibraryKnowledge(c.Request.Context(), actor(c), c.Param("id"), input.Version, input.Tags)
		if err != nil {
			competitionError(c, err)
			return
		}
		ok(c, 200, gin.H{"version": version})
	})
	admin.GET("/knowledge-points", func(c *gin.Context) {
		items, err := s.competitions().KnowledgePoints(c.Request.Context())
		if err != nil {
			competitionError(c, err)
			return
		}
		ok(c, 200, gin.H{"items": items})
	})
	admin.POST("/knowledge-points", func(c *gin.Context) {
		var input struct {
			Name   string `json:"name"`
			Owner  string `json:"owner_type"`
			Parent string `json:"parent_name"`
		}
		if !bindCompetition(c, &input) {
			return
		}
		if err := s.competitions().SetKnowledge(c.Request.Context(), actor(c), input.Name, input.Parent, true, input.Owner); err != nil {
			competitionError(c, err)
			return
		}
		ok(c, 201, gin.H{"created": true})
	})
	admin.PUT("/knowledge-points/:name/parent", func(c *gin.Context) {
		var input struct {
			Parent string `json:"parent_name"`
		}
		if !bindCompetition(c, &input) {
			return
		}
		if err := s.competitions().SetKnowledge(c.Request.Context(), actor(c), c.Param("name"), input.Parent, false); err != nil {
			competitionError(c, err)
			return
		}
		ok(c, 200, gin.H{"updated": true})
	})
	admin.PUT("/knowledge-points/:name/types", func(c *gin.Context) {
		var input struct {
			Types []string `json:"types"`
		}
		if !bindCompetition(c, &input) {
			return
		}
		if err := s.competitions().LinkKnowledgeTypes(c.Request.Context(), actor(c), c.Param("name"), input.Types); err != nil {
			competitionError(c, err)
			return
		}
		ok(c, 200, gin.H{"updated": true})
	})
}
