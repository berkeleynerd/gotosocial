// GoToSocial
// Copyright (C) GoToSocial Authors admin@gotosocial.org
// SPDX-License-Identifier: AGPL-3.0-or-later
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package admin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	apimodel "github.com/superseriousbusiness/gotosocial/internal/api/model"
	apiutil "github.com/superseriousbusiness/gotosocial/internal/api/util"
	"github.com/superseriousbusiness/gotosocial/internal/gtserror"
	"github.com/superseriousbusiness/gotosocial/internal/oauth"
	"github.com/superseriousbusiness/gotosocial/internal/util"
)

// DomainPermissionSubscriptionPOSTHandler swagger:operation POST /api/v1/admin/domain_permission_subscriptions domainPermissionSubscriptionCreate
//
// Create a domain permission subscription with the given parameters.
//
//	---
//	tags:
//	- admin
//
//	consumes:
//	- multipart/form-data
//	- application/json
//
//	produces:
//	- application/json
//
//	parameters:
//	-
//		name: priority
//		in: formData
//		description: >-
//			Priority of this subscription compared to others of the same permission type.
//			0-255 (higher = higher priority). Higher priority subscriptions will overwrite
//			permissions generated by lower priority subscriptions. When two subscriptions
//			have the same `priority` value, priority is indeterminate, so it's recommended
//			to always set this value manually.
//		type: number
//		minimum: 0
//		maximum: 255
//		default: 0
//	-
//		name: title
//		in: formData
//		description: Optional title for this subscription.
//		type: string
//	-
//		name: permission_type
//		required: true
//		in: formData
//		description: >-
//			Type of permissions to create by parsing the targeted file/list.
//			One of "allow" or "block".
//		type: string
//	-
//		name: as_draft
//		in: formData
//		description: >-
//			If true, domain permissions arising from this subscription will be
//			created as drafts that must be approved by a moderator to take effect.
//			If false, domain permissions from this subscription will come into force immediately.
//			Defaults to "true".
//		type: boolean
//		default: true
//	-
//		name: adopt_orphans
//		in: formData
//		description: >-
//			If true, this domain permission subscription will "adopt" domain permissions
//			which already exist on the instance, and which meet the following conditions:
//			1) they have no subscription ID (ie., they're "orphaned") and 2) they are present
//			in the subscribed list. Such orphaned domain permissions will be given this
//			subscription's subscription ID value and be managed by this subscription.
//		type: boolean
//		default: false
//	-
//		name: uri
//		required: true
//		in: formData
//		description: URI to call in order to fetch the permissions list.
//		type: string
//	-
//		name: content_type
//		required: true
//		in: formData
//		description: >-
//			MIME content type to use when parsing the permissions list.
//			One of "text/plain", "text/csv", and "application/json".
//		type: string
//	-
//		name: fetch_username
//		in: formData
//		description: >-
//			Optional basic auth username to provide when fetching given uri.
//			If set, will be transmitted along with `fetch_password` when doing the fetch.
//		type: string
//	-
//		name: fetch_password
//		in: formData
//		description: >-
//			Optional basic auth password to provide when fetching given uri.
//			If set, will be transmitted along with `fetch_username` when doing the fetch.
//		type: string
//
//	security:
//	- OAuth2 Bearer:
//		- admin
//
//	responses:
//		'200':
//			description: The newly created domain permission subscription.
//			schema:
//				"$ref": "#/definitions/domainPermissionSubscription"
//		'400':
//			description: bad request
//		'401':
//			description: unauthorized
//		'403':
//			description: forbidden
//		'406':
//			description: not acceptable
//		'409':
//			description: conflict
//		'500':
//			description: internal server error
func (m *Module) DomainPermissionSubscriptionPOSTHandler(c *gin.Context) {
	authed, err := oauth.Authed(c, true, true, true, true)
	if err != nil {
		apiutil.ErrorHandler(c, gtserror.NewErrorUnauthorized(err, err.Error()), m.processor.InstanceGetV1)
		return
	}

	if !*authed.User.Admin {
		err := fmt.Errorf("user %s not an admin", authed.User.ID)
		apiutil.ErrorHandler(c, gtserror.NewErrorForbidden(err, err.Error()), m.processor.InstanceGetV1)
		return
	}

	if authed.Account.IsMoving() {
		apiutil.ForbiddenAfterMove(c)
		return
	}

	if _, err := apiutil.NegotiateAccept(c, apiutil.JSONAcceptHeaders...); err != nil {
		apiutil.ErrorHandler(c, gtserror.NewErrorNotAcceptable(err, err.Error()), m.processor.InstanceGetV1)
		return
	}

	// Parse + validate form.
	form := new(apimodel.DomainPermissionSubscriptionRequest)
	if err := c.ShouldBind(form); err != nil {
		apiutil.ErrorHandler(c, gtserror.NewErrorBadRequest(err, err.Error()), m.processor.InstanceGetV1)
		return
	}

	// Check priority.
	// Default to 0.
	priority := util.PtrOrZero(form.Priority)
	if priority < 0 || priority > 255 {
		const errText = "priority must be a number in the range 0 to 255"
		errWithCode := gtserror.NewErrorBadRequest(errors.New(errText), errText)
		apiutil.ErrorHandler(c, errWithCode, m.processor.InstanceGetV1)
		return
	}

	// Ensure URI is set.
	if form.URI == nil {
		const errText = "uri must be set"
		errWithCode := gtserror.NewErrorBadRequest(errors.New(errText), errText)
		apiutil.ErrorHandler(c, errWithCode, m.processor.InstanceGetV1)
		return
	}

	// Ensure URI is parseable.
	uri, err := url.Parse(*form.URI)
	if err != nil {
		err := fmt.Errorf("invalid uri provided: %w", err)
		errWithCode := gtserror.NewErrorBadRequest(err, err.Error())
		apiutil.ErrorHandler(c, errWithCode, m.processor.InstanceGetV1)
		return
	}

	// Normalize URI by converting back to string.
	uriStr := uri.String()

	// Content type must be set.
	contentTypeStr := util.PtrOrZero(form.ContentType)
	contentType, errWithCode := parseDomainPermSubContentType(contentTypeStr)
	if errWithCode != nil {
		apiutil.ErrorHandler(c, errWithCode, m.processor.InstanceGetV1)
		return
	}

	// Permission type must be set.
	permTypeStr := util.PtrOrZero(form.PermissionType)
	permType, errWithCode := parseDomainPermissionType(permTypeStr)
	if errWithCode != nil {
		apiutil.ErrorHandler(c, errWithCode, m.processor.InstanceGetV1)
		return
	}

	// Default `as_draft` to true.
	asDraft := util.PtrOrValue(form.AsDraft, true)

	permSub, errWithCode := m.processor.Admin().DomainPermissionSubscriptionCreate(
		c.Request.Context(),
		authed.Account,
		uint8(priority),            // #nosec G115 -- Validated above.
		util.PtrOrZero(form.Title), // Optional.
		uriStr,
		contentType,
		permType,
		asDraft,
		util.PtrOrZero(form.FetchUsername), // Optional.
		util.PtrOrZero(form.FetchPassword), // Optional.
	)
	if errWithCode != nil {
		apiutil.ErrorHandler(c, errWithCode, m.processor.InstanceGetV1)
		return
	}

	apiutil.JSON(c, http.StatusOK, permSub)
}