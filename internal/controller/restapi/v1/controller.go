package v1

import (
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/go-playground/validator/v10"
)

// V1 -.
type V1 struct {
	t                  usecase.Translation
	u                  usecase.User
	tk                 usecase.Task
	reader             usecase.Reader
	bookRAG            usecase.BookRAG
	quran              usecase.Quran
	anchor             usecase.AnchorResolver
	crossReference     usecase.CrossReference
	unitRegistry       usecase.UnitRegistry
	personal           usecase.Personal
	editorial          usecase.Editorial
	email              usecase.EmailAdmin
	emailWebhookSecret string
	l                  logger.Interface
	v                  *validator.Validate
}
