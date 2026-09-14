package handlers

import (
	"github.com/go-fuego/fuego"

	"capv-proxy/pkg/strategies"
)

type ClusterTagHandler struct {
	tagService strategies.ForManagingClusterTags
}

type ClusterCreateTagParams struct {
	Value string `json:"value" validate:"required"`
}

type ClusterTagsResponse struct {
	Result map[string]string `json:"result"`
}

func NewClusterTagHandler(tagService strategies.ForManagingClusterTags) ClusterTagHandler {
	return ClusterTagHandler{tagService: tagService}
}

func (k ClusterTagHandler) GetClusterTags(c fuego.ContextNoBody) (ClusterTagsResponse, error) {
	return ClusterTagsResponse{Result: k.tagService.GetClusterTags(c.Context(), c.PathParam("id"))}, nil
}

func (k ClusterTagHandler) GetClusterTag(c fuego.ContextNoBody) (string, error) {
	return k.tagService.GetClusterTag(c.Context(), c.PathParam("id"), c.PathParam("tag")), nil
}

func (k ClusterTagHandler) CreateOrUpdateClusterTag(c fuego.ContextWithBody[ClusterCreateTagParams]) (string, error) {
	createParams, err := c.Body()
	if err != nil {
		return err.Error(), err
	}

	k.tagService.SetClusterTag(c.Context(), c.PathParam("id"), c.PathParam("tag"), createParams.Value)

	return "ok", nil
}

func (k ClusterTagHandler) DeleteClusterTag(c fuego.ContextNoBody) (string, error) {
	k.tagService.DeleteClusterTag(c.Context(), c.PathParam("id"), c.PathParam("tag"))

	return "ok", nil
}
