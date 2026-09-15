package handlers

import (
	"github.com/go-fuego/fuego"

	"capv-proxy/pkg/strategies"
)

type ClusterTagHandler struct {
	tagService strategies.ForManagingClusterTags
}

type ClusterCreateTagParams struct {
	// Kubernetes limits label values to 63 characters:
	// https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set
	Value string `json:"value" validate:"max=63"`
}

type ClusterTagsResponse struct {
	Result map[string]string `json:"result"`
}

func NewClusterTagHandler(tagService strategies.ForManagingClusterTags) ClusterTagHandler {
	return ClusterTagHandler{tagService: tagService}
}

func (k ClusterTagHandler) GetClusterTags(c fuego.ContextNoBody) (ClusterTagsResponse, error) {
	tags, err := k.tagService.GetClusterTags(c.Context(), c.PathParam("id"))
	if err != nil {
		return ClusterTagsResponse{}, toHTTPError(err)
	}
	return ClusterTagsResponse{Result: tags}, nil
}

func (k ClusterTagHandler) GetClusterTag(c fuego.ContextNoBody) (string, error) {
	value, err := k.tagService.GetClusterTag(c.Context(), c.PathParam("id"), c.PathParam("tag"))
	if err != nil {
		return "", toHTTPError(err)
	}
	return value, nil
}

func (k ClusterTagHandler) CreateOrUpdateClusterTag(c fuego.ContextWithBody[ClusterCreateTagParams]) (string, error) {
	createParams, err := c.Body()
	if err != nil {
		return err.Error(), err
	}

	if err := k.tagService.SetClusterTag(c.Context(), c.PathParam("id"), c.PathParam("tag"), createParams.Value); err != nil {
		return "", toHTTPError(err)
	}

	return "ok", nil
}

func (k ClusterTagHandler) DeleteClusterTag(c fuego.ContextNoBody) (string, error) {
	if err := k.tagService.DeleteClusterTag(c.Context(), c.PathParam("id"), c.PathParam("tag")); err != nil {
		return "", toHTTPError(err)
	}

	return "ok", nil
}
