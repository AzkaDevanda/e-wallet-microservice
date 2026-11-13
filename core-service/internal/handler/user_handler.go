package handler

import (
	"core-service/internal/dto"
	"core-service/internal/service"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

type UserHandler struct {
	userService service.UserService
}

func NewUserHandler(userService service.UserService) *UserHandler {
	return &UserHandler{userService: userService}
}

// Login menangani POST /users/login
func (h *UserHandler) Login(c *gin.Context) {
	var req dto.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}

	token, err := h.userService.Login(c.Request.Context(), req)
	if errors.Is(err, service.ErrInvalidCredentials) {
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		log.Printf("login error: %v", err)
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "internal server error"})
		return
	}

	c.JSON(http.StatusOK, dto.LoginResponse{Token: token})
}

// Register implements [service.UserService].
func (h *UserHandler) Register(c *gin.Context) {
	var req dto.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}

	user, err := h.userService.Register(c.Request.Context(), req)
	if errors.Is(err, service.ErrEmailAlreadyExists) {
		c.JSON(http.StatusConflict, dto.ErrorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		log.Printf("register error: %v", err)
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "internal server error"})
		return
	}

	c.JSON(http.StatusCreated, dto.RegisterResponse{
		UserID:  user.ID.String(),
		Name:    user.Name,
		Email:   user.Email,
		Message: "User registered successfully",
	})
}
