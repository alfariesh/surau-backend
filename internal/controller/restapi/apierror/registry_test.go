package apierror

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// goldenCodes is the deliberate DUPLICATE of frozenCodes: changing an entry
// in registry.go without consciously updating this copy fails the contract.
// A red diff here means an error code shipped to FE/mobile is about to move —
// that is a breaking change and needs its own decision, not a spelling fix.
//
//nolint:gochecknoglobals,gosec // golden copy of the frozen contract; message strings, not credentials
var goldenCodes = map[string]string{
	"missing authorization header":        "AUTH_HEADER_MISSING",
	"invalid authorization header format": "AUTH_HEADER_INVALID",
	"invalid or expired token":            "AUTH_TOKEN_INVALID",
	"unauthorized":                        "AUTH_UNAUTHORIZED",
	"invalid credentials":                 "AUTH_INVALID_CREDENTIALS",
	"email not verified":                  "AUTH_EMAIL_NOT_VERIFIED",
	"too many auth attempts":              "AUTH_RATE_LIMITED",

	"active khatam cycle already exists":   "active_khatam_cycle_already_exists",
	"anchor not found":                     "anchor_not_found",
	"book not found":                       "book_not_found",
	"citable unit not found":               "citable_unit_not_found",
	"invalid mfa challenge":                "invalid_mfa_challenge",
	"invalid mfa code":                     "invalid_mfa_code",
	"invalid mfa reset":                    "invalid_mfa_reset",
	"mfa already enabled":                  "mfa_already_enabled",
	"mfa enrollment not started":           "mfa_enrollment_not_started",
	"mfa enrollment required":              "mfa_enrollment_required",
	"mfa not enabled":                      "mfa_not_enabled",
	"mfa step-up required":                 "mfa_step_up_required",
	"cannot change own role":               "cannot_change_own_role",
	"cannot demote the last admin":         "cannot_demote_the_last_admin",
	"cross-reference already exists":       "cross_reference_already_exists",
	"cross-reference not found":            "cross_reference_not_found",
	"database problems":                    "database_problems",
	"draft not found":                      "draft_not_found",
	"email delivery failed":                "email_delivery_failed",
	"empty batch":                          "empty_batch",
	"feedback not found":                   "feedback_not_found",
	"forbidden":                            "forbidden",
	"heading not found":                    "heading_not_found",
	"if-match header required":             "if_match_header_required",
	"internal server error":                "internal_server_error",
	"invalid activity date":                "invalid_activity_date",
	"invalid activity range":               "invalid_activity_range",
	"invalid anchor":                       "invalid_anchor",
	"invalid asset type":                   "invalid_asset_type",
	"invalid asset_type":                   "invalid_asset_type",
	"invalid author_id":                    "invalid_author_id",
	"invalid ayah key":                     "invalid_ayah_key",
	"invalid book_id":                      "invalid_book_id",
	"invalid category_id":                  "invalid_category_id",
	"invalid collection slug":              "invalid_collection_slug",
	"invalid cross-reference":              "invalid_cross_reference",
	"invalid email_verified":               "invalid_email_verified",
	"invalid feedback":                     "invalid_feedback",
	"invalid feedback id":                  "invalid_feedback_id",
	"invalid from":                         "invalid_from",
	"invalid has_content":                  "invalid_has_content",
	"invalid heading_id":                   "invalid_heading_id",
	"invalid hizb_number":                  "invalid_hizb_number",
	"invalid include_audio":                "invalid_include_audio",
	"invalid include_info":                 "invalid_include_info",
	"invalid include_quran_references":     "invalid_include_quran_references",
	"invalid include_translation":          "invalid_include_translation",
	"invalid juz_number":                   "invalid_juz_number",
	"invalid license status":               "invalid_license_status",
	"invalid needs_work":                   "invalid_needs_work",
	"invalid page_id":                      "invalid_page_id",
	"invalid page_number":                  "invalid_page_number",
	"invalid production draft":             "invalid_production_draft",
	"invalid question":                     "invalid_question",
	"invalid quran progress":               "invalid_quran_progress",
	"invalid quran range":                  "invalid_quran_range",
	"invalid reader location":              "invalid_reader_location",
	"invalid reading progress":             "invalid_reading_progress",
	"invalid ready_to_publish":             "invalid_ready_to_publish",
	"invalid refresh token":                "invalid_refresh_token",
	"invalid request":                      "invalid_request",
	"invalid request body":                 "invalid_request_body",
	"invalid review decision":              "invalid_review_decision",
	"invalid role":                         "invalid_role",
	"invalid saved item":                   "invalid_saved_item",
	"invalid service token":                "invalid_service_token",
	"invalid since":                        "invalid_since",
	"invalid status":                       "invalid_status",
	"invalid status transition":            "invalid_status_transition",
	"invalid surah_id":                     "invalid_surah_id",
	"invalid task status":                  "invalid_task_status",
	"invalid to":                           "invalid_to",
	"invalid unstarted":                    "invalid_unstarted",
	"invalid unsubscribe token":            "invalid_unsubscribe_token",
	"invalid user preference":              "invalid_user_preference",
	"invalid verification token":           "invalid_verification_token",
	"invalid view":                         "invalid_view",
	"khatam cycle incomplete":              "khatam_cycle_incomplete",
	"license not permitted":                "license_not_permitted",
	"khatam cycle not found":               "khatam_cycle_not_found",
	"not found":                            "not_found",
	"page not found":                       "page_not_found",
	"password reset email recently sent":   "password_reset_email_recently_sent",
	"precondition failed":                  "precondition_failed",
	"production project already exists":    "production_project_already_exists",
	"production project is not ready":      "production_project_is_not_ready",
	"production project not found":         "production_project_not_found",
	"progress not found":                   "progress_not_found",
	"quran ayah not found":                 "quran_ayah_not_found",
	"quran is not configured":              "quran_is_not_configured",
	"quran navigation not found":           "quran_navigation_not_found",
	"quran recitation not found":           "quran_recitation_not_found",
	"quran source attribution is required": "quran_source_attribution_is_required",
	"quran source not found":               "quran_source_not_found",
	"quran surah not found":                "quran_surah_not_found",
	"quran translation source not found":   "quran_translation_source_not_found",
	"rag evidence not found":               "rag_evidence_not_found",
	"rag is not configured":                "rag_is_not_configured",
	"rag llm is not configured":            "rag_llm_is_not_configured",
	"rag unit materialization incomplete":  "rag_unit_materialization_incomplete",
	"rag unit materialization stale":       "rag_unit_materialization_stale",
	"saved item not found":                 "saved_item_not_found",
	"session not found":                    "session_not_found",
	"task not found":                       "task_not_found",
	"translation not found":                "translation_not_found",
	"translation service problems":         "translation_service_problems",
	"unsupported language":                 "unsupported_language",
	"user already exists":                  "user_already_exists",
	"user not found":                       "user_not_found",
	"verification email recently sent":     "verification_email_recently_sent",

	"email message not resendable": "email_message_not_resendable",
	"email recipient suppressed":   "email_recipient_suppressed",
	"invalid auth input":           "invalid_auth_input",
	"invalid email campaign":       "invalid_email_campaign",
	"invalid email template":       "invalid_email_template",

	"too many requests":        "too_many_requests",
	"method not allowed":       "method_not_allowed",
	"request entity too large": "request_entity_too_large",
}

// TestFrozenCodesMatchGolden freezes the registry: exact same keys, exact
// same values, nothing missing, nothing extra.
func TestFrozenCodesMatchGolden(t *testing.T) {
	t.Parallel()

	for msg, want := range goldenCodes {
		got, ok := frozenCodes[msg]
		if !ok {
			t.Errorf("frozenCodes missing entry %q (present in golden copy)", msg)

			continue
		}

		if got != want {
			t.Errorf("frozenCodes[%q] = %q, golden says %q — an already-shipped code must never change", msg, got, want)
		}
	}

	for msg := range frozenCodes {
		if _, ok := goldenCodes[msg]; !ok {
			t.Errorf("frozenCodes has unregistered entry %q — add it to the golden copy DELIBERATELY", msg)
		}
	}
}

// TestFrozenCodesResolveViaCode pins the public entry point: every frozen
// message resolves to its frozen code through Code(), case-insensitively.
func TestFrozenCodesResolveViaCode(t *testing.T) {
	t.Parallel()

	for msg, want := range frozenCodes {
		if got := Code(msg); got != want {
			t.Errorf("Code(%q) = %q, want frozen %q", msg, got, want)
		}

		if got := Code(strings.ToUpper(msg)); got != want {
			t.Errorf("Code(upper %q) = %q, want frozen %q (lookup must stay case-insensitive)", msg, got, want)
		}
	}
}

// TestDerivationStillMatchesFrozenSnapshot guards the fallback algorithm:
// for every non-explicit entry the historical derivation must still produce
// the frozen value, so silently changing derive() cannot go unnoticed.
func TestDerivationStillMatchesFrozenSnapshot(t *testing.T) {
	t.Parallel()

	for msg, want := range frozenCodes {
		if strings.ToUpper(want) == want {
			// Explicit SCREAMING_SNAKE auth codes never came from derive().
			continue
		}

		if got := derive(msg); got != want {
			t.Errorf("derive(%q) = %q, frozen snapshot %q — derivation algorithm drifted", msg, got, want)
		}
	}
}

// TestEveryEmittedMessageLiteralIsRegistered is the AC-1 enforcement (F1-D):
// it AST-scans the whole restapi tree for message literals passed to the
// error emitters and fails when any literal is missing from frozenCodes.
// Editing an error sentence therefore breaks this test until the new
// sentence is deliberately registered — the old code stays frozen.
func TestEveryEmittedMessageLiteralIsRegistered(t *testing.T) {
	t.Parallel()

	literals, err := collectEmittedMessageLiterals("..")
	if err != nil {
		t.Fatalf("scan restapi tree: %v", err)
	}

	if len(literals) < 90 {
		t.Fatalf("scanner found only %d literals — scan roots or emitter names look broken", len(literals))
	}

	for _, msg := range literals {
		if _, ok := frozenCodes[msg]; !ok {
			t.Errorf("error message %q is emitted but not registered in apierror.frozenCodes — register it (new entry, never repurpose an old one)", msg)
		}
	}
}

// messageArgIndex: emitter function name → index of the message argument.
//
//nolint:gochecknoglobals // test-only scanner configuration
var messageArgIndex = map[string]int{
	"errorResponse":                     2,
	"errorResponseWithDetails":          2,
	"middlewareError":                   2,
	"adminErrorResponse":                2,
	"ProductionPublishBlockedFromCheck": 0,
}

func collectEmittedMessageLiterals(root string) ([]string, error) {
	seen := map[string]bool{}

	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}

		fset := token.NewFileSet()

		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}

		ast.Inspect(file, func(n ast.Node) bool {
			collectFromNode(n, seen)

			return true
		})

		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	literals := make([]string, 0, len(seen))
	for msg := range seen {
		literals = append(literals, msg)
	}

	return literals, nil
}

func collectFromNode(n ast.Node, seen map[string]bool) {
	switch node := n.(type) {
	case *ast.CallExpr:
		idx, ok := messageArgIndex[calleeName(node)]
		if !ok || len(node.Args) <= idx {
			return
		}

		addStringLit(node.Args[idx], seen)
	case *ast.CompositeLit:
		if typeName(node.Type) != "ProductionProjectConflict" {
			return
		}

		for _, elt := range node.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}

			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Error" {
				addStringLit(kv.Value, seen)
			}
		}
	}
}

func addStringLit(expr ast.Expr, seen map[string]bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return
	}

	if s, err := strconv.Unquote(lit.Value); err == nil && s != "" {
		seen[strings.ToLower(s)] = true
	}
}

func calleeName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}

	return ""
}

func typeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	}

	return ""
}
