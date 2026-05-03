package handler

import (
	"github.com/mikaelstaldal/mymail/internal/api"
)

type Handler struct {
	api.UnimplementedHandler
}

var _ api.Handler = (*Handler)(nil)
