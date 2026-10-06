package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) adminProtocolUpgradeStatus(c *gin.Context) {
	receipt, err := s.store.ProtocolUpgradeStatus(c.Request.Context())
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, receipt)
}

func (s *Server) adminProtocolUpgradePreview(c *gin.Context) {
	var input protocolUpgradeInput
	if err := decodeProtocolAdminBody(c, &input); err != nil {
		respondProtocolError(c, err)
		return
	}
	preview, err := s.prepareProtocolUpgrade(c.Request.Context(), input)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, preview)
}

func (s *Server) adminProtocolUpgradeApply(c *gin.Context) {
	var input protocolUpgradeInput
	if err := decodeProtocolAdminBody(c, &input); err != nil {
		respondProtocolError(c, err)
		return
	}
	if input.Baseline == "" {
		respondFail(c, http.StatusBadRequest, "missing_baseline", "apply requires the baseline from a reviewed migration preview")
		return
	}
	receipt, err := s.applyProtocolUpgrade(c.Request.Context(), input)
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	err = s.reloadProtocolRuntime(c.Request.Context())
	if err != nil {
		respondProtocolError(c, err)
		return
	}
	respondOK(c, receipt)
}
