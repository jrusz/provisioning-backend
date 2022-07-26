package sources

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/RHEnVision/provisioning-backend/internal/cache"
	"github.com/RHEnVision/provisioning-backend/internal/config"
	"github.com/RHEnVision/provisioning-backend/internal/ctxval"
	"github.com/RHEnVision/provisioning-backend/internal/headers"
	"github.com/RHEnVision/provisioning-backend/internal/parsing"
)

type SourcesClient struct {
	client *ClientWithResponses
}

func init() {
	GetSourcesClientV2 = newSourcesClient
}

func newSourcesClient(ctx context.Context) (ClientV2, error) {
	c, err := NewClientWithResponses(config.Sources.URL)
	if err != nil {
		return nil, err
	}
	return &SourcesClient{client: c}, nil
}

func (c *SourcesClient) ListProvisioningSources(ctx context.Context) (*[]Source, error) {
	ctxval.Logger(ctx).Info().Msg("Listing provisioning sources")
	appTypeId, err := c.GetProvisioningTypeId(ctx)
	if err != nil {
		ctxval.Logger(ctx).Warn().Err(err).Msg("Failed to get provisioning type id")
		return nil, fmt.Errorf("failed to get provisioning app type: %w", err)
	}
	resp, err := c.client.ListApplicationTypeSourcesWithResponse(ctx, appTypeId, &ListApplicationTypeSourcesParams{}, headers.AddIdentityHeader)
	if err != nil {
		ctxval.Logger(ctx).Warn().Err(err).Msg("Failed to fetch ApplicationTypes from sources")
		return nil, fmt.Errorf("failed to get ApplicationTypes: %w", err)
	}
	statusCode := resp.StatusCode()

	if parsing.IsHTTPNotFound(statusCode) {
		return nil, NewNotFoundError(ctx, "sources not found")
	}
	if !parsing.IsHTTPStatus2xx(statusCode) {
		ctxval.Logger(ctx).Warn().Msgf("Sources replied with unexpected status while fetching sources of application type: %v", statusCode)
		return nil, SourcesClientErr
	}
	return resp.JSON200.Data, nil
}

func (c *SourcesClient) GetArn(ctx context.Context, sourceId string) (string, error) {
	ctxval.Logger(ctx).Info().Msgf("Getting ARN of source %v", sourceId)
	// Get all the authentications linked to a specific source
	resp, err := c.client.ListSourceAuthenticationsWithResponse(ctx, sourceId, &ListSourceAuthenticationsParams{}, headers.AddIdentityHeader)
	if err != nil {
		return "", fmt.Errorf("cannot list source authentication: %w", err)
	}
	statusCode := resp.StatusCode()
	if parsing.IsHTTPNotFound(statusCode) {
		return "", NewNotFoundError(ctx, "authentications for source weren't found in sources app")
	}
	if !parsing.IsHTTPStatus2xx(statusCode) {
		ctxval.Logger(ctx).Warn().Msgf("Sources replied with unexpected status while fetching Authentications per source: %v", statusCode)
		return "", SourcesClientErr
	}
	// Filter authentications to include only auth where resource_type == "Application"
	auth, err := filterSourceAuthentications(resp.JSON200.Data)
	if err != nil {
		ctxval.Logger(ctx).Warn().Msgf("Sources replied with more then one authenticatios for source: %vs", sourceId)
		return "", err
	}
	// Get the resource_id which equals to application_id
	// and check that application_type_id in /applications/<app_id> equals to provisioning id
	res, err := c.client.ShowApplicationWithResponse(ctx, *auth.ResourceId, headers.AddIdentityHeader)
	if err != nil {
		return "", fmt.Errorf("cannot list source authentication: %w", err)
	}
	statusCode = res.StatusCode()
	if parsing.IsHTTPNotFound(statusCode) {
		return "", NewNotFoundError(ctx, "application not found is sources app")
	}
	if !parsing.IsHTTPStatus2xx(statusCode) {
		ctxval.Logger(ctx).Warn().Msgf("Sources replied with unexpected status while fetching Applications: %v", statusCode)
		return "", SourcesClientErr
	}

	appTypeId, err := c.GetProvisioningTypeId(ctx)
	if err != nil {
		return "", fmt.Errorf("cannot get provisioning app type: %w", err)
	}

	if *res.JSON200.ApplicationTypeId == appTypeId {
		return *auth.Username, nil

	}
	return "", fmt.Errorf("cannot find authentication linked to source id %s and to the provisioning app: %w", sourceId, err)
}

func (c *SourcesClient) GetProvisioningTypeId(ctx context.Context) (string, error) {
	if appTypeId, ok := cache.AppTypeId(); ok {
		return appTypeId, nil
	}
	appTypeId, err := c.loadAppId(ctx)
	if err != nil {
		return "", err
	}
	cache.SetAppTypeId(appTypeId)
	return appTypeId, nil
}

func (c *SourcesClient) loadAppId(ctx context.Context) (string, error) {
	ctxval.Logger(ctx).Info().Msg("Fetching the Application Type ID of Provisioning for Sources")
	resp, err := c.client.ListApplicationTypes(ctx, &ListApplicationTypesParams{})
	if err != nil {
		ctxval.Logger(ctx).Warn().Err(err).Msg("Failed to fetch ApplicationTypes from sources")
		return "", fmt.Errorf("failed to fetch ApplicationTypes: %w", err)
	}
	if !parsing.IsHTTPStatus2xx(resp.StatusCode) {
		ctxval.Logger(ctx).Warn().Msgf("Sources replied with unexpected status while fetching ApplicationTypes: %v", resp.Status)
		return "", fmt.Errorf("%w, status: '%s'", ApplicationTypesFetchUnsuccessful, resp.Status)
	}
	defer resp.Body.Close()
	var appTypesData dataElement
	if err = json.NewDecoder(resp.Body).Decode(&appTypesData); err != nil {
		return "", fmt.Errorf("could not unmarshal the response: %w", err)
	}
	for _, t := range appTypesData.Data {
		if t.Name == "/insights/platform/provisioning" {
			ctxval.Logger(ctx).Info().Msgf("The Application Type ID found: '%s' and it got cached", t.Id)
			return t.Id, nil
		}
	}
	return "", NewNotFoundError(ctx, "application type 'provisioning' has not been found in types supported by sources")
}

func filterSourceAuthentications(authentications *[]AuthenticationRead) (AuthenticationRead, error) {
	auths := *authentications
	list := make([]AuthenticationRead, 0, len(auths))
	for _, auth := range auths {
		if *auth.ResourceType == "Application" {
			list = append(list, auth)
		}
	}
	// Assumption: each source has one authentication linked to it
	if len(list) > 1 {
		return AuthenticationRead{}, MoreThenOneAuthenticationForSourceErr
	}
	return list[0], nil
}