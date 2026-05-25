package reader

import (
	"context"
	"strings"

	"github.com/evrone/go-clean-template/internal/entity"
)

// TOC returns the nested table of contents for a published book.
func (uc *UseCase) TOC(
	ctx context.Context,
	bookID int,
	lang string,
	includeAudio bool,
) ([]entity.BookTOCNode, error) {
	entries, err := uc.repo.ListTOCEntries(ctx, bookID, normalizeLang(lang), includeAudio)
	if err != nil {
		return nil, err
	}

	return buildTOCTree(entries), nil
}

// TOCRead returns one TOC section as an article-like reader response.
func (uc *UseCase) TOCRead(ctx context.Context, bookID, headingID int, lang string) (entity.BookTOCRead, error) {
	lang = normalizeLang(lang)

	entries, err := uc.repo.ListTOCEntries(ctx, bookID, lang, true)
	if err != nil {
		return entity.BookTOCRead{}, err
	}

	context, ok := buildTOCContext(entries, headingID)
	if !ok {
		return entity.BookTOCRead{}, entity.ErrHeadingNotFound
	}

	section, err := uc.repo.GetSection(ctx, bookID, headingID, lang)
	if err != nil {
		return entity.BookTOCRead{}, err
	}

	return entity.BookTOCRead{
		BookID:       bookID,
		HeadingID:    headingID,
		Title:        context.current.Title,
		Breadcrumb:   context.breadcrumb,
		Children:     context.children,
		Previous:     context.previous,
		Next:         context.next,
		StartPageID:  section.StartPageID,
		EndPageID:    section.EndPageID,
		OriginalHTML: section.OriginalHTML,
		OriginalText: section.OriginalText,
		Translation:  section.Translation,
		Audio:        section.Audio,
	}, nil
}

// TOCPlaylist returns a continuous audiobook manifest for one TOC subtree.
func (uc *UseCase) TOCPlaylist(ctx context.Context, bookID, headingID int, lang string) (entity.BookTOCPlaylist, error) {
	lang = normalizeLang(lang)

	entries, err := uc.repo.ListTOCEntries(ctx, bookID, lang, true)
	if err != nil {
		return entity.BookTOCPlaylist{}, err
	}

	return buildTOCPlaylist(entries, bookID, headingID, lang)
}

type tocContext struct {
	current    entity.BookTOCEntry
	breadcrumb []entity.BookTOCLink
	children   []entity.BookTOCLink
	previous   *entity.BookTOCLink
	next       *entity.BookTOCLink
}

func normalizeLang(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		return "id"
	}

	return lang
}

func buildTOCTree(entries []entity.BookTOCEntry) []entity.BookTOCNode {
	byID := make(map[int]entity.BookTOCEntry, len(entries))
	childrenByParent := make(map[int][]int, len(entries))
	rootIDs := make([]int, 0)

	for _, entry := range entries {
		byID[entry.HeadingID] = entry
	}

	for _, entry := range entries {
		if entry.ParentID != nil {
			if _, ok := byID[*entry.ParentID]; ok {
				childrenByParent[*entry.ParentID] = append(childrenByParent[*entry.ParentID], entry.HeadingID)
				continue
			}
		}

		rootIDs = append(rootIDs, entry.HeadingID)
	}

	var buildNode func(int) entity.BookTOCNode
	buildNode = func(headingID int) entity.BookTOCNode {
		entry := byID[headingID]
		childIDs := childrenByParent[headingID]
		node := tocNodeFromEntry(entry)
		node.Children = make([]entity.BookTOCNode, 0, len(childIDs))

		for _, childID := range childIDs {
			node.Children = append(node.Children, buildNode(childID))
		}

		return node
	}

	nodes := make([]entity.BookTOCNode, 0, len(rootIDs))
	for _, rootID := range rootIDs {
		nodes = append(nodes, buildNode(rootID))
	}

	return nodes
}

func buildTOCContext(entries []entity.BookTOCEntry, headingID int) (tocContext, bool) {
	byID := make(map[int]entity.BookTOCEntry, len(entries))
	childrenByParent := make(map[int][]entity.BookTOCEntry, len(entries))
	currentIndex := -1

	for i, entry := range entries {
		byID[entry.HeadingID] = entry
		if entry.ParentID != nil {
			childrenByParent[*entry.ParentID] = append(childrenByParent[*entry.ParentID], entry)
		}

		if entry.HeadingID == headingID {
			currentIndex = i
		}
	}

	if currentIndex < 0 {
		return tocContext{}, false
	}

	current := entries[currentIndex]
	breadcrumb := make([]entity.BookTOCLink, 0)
	for parentID := current.ParentID; parentID != nil; {
		parent, ok := byID[*parentID]
		if !ok {
			break
		}

		breadcrumb = append(breadcrumb, tocLinkFromEntry(parent))
		parentID = parent.ParentID
	}

	reverseLinks(breadcrumb)

	children := make([]entity.BookTOCLink, 0, len(childrenByParent[headingID]))
	for _, child := range childrenByParent[headingID] {
		children = append(children, tocLinkFromEntry(child))
	}

	var previous *entity.BookTOCLink
	if currentIndex > 0 {
		link := tocLinkFromEntry(entries[currentIndex-1])
		previous = &link
	}

	var next *entity.BookTOCLink
	if currentIndex+1 < len(entries) {
		link := tocLinkFromEntry(entries[currentIndex+1])
		next = &link
	}

	return tocContext{
		current:    current,
		breadcrumb: breadcrumb,
		children:   children,
		previous:   previous,
		next:       next,
	}, true
}

func buildTOCPlaylist(
	entries []entity.BookTOCEntry,
	bookID int,
	headingID int,
	lang string,
) (entity.BookTOCPlaylist, error) {
	start := -1
	for i, entry := range entries {
		if entry.HeadingID == headingID {
			start = i
			break
		}
	}

	if start < 0 {
		return entity.BookTOCPlaylist{}, entity.ErrHeadingNotFound
	}

	end := len(entries)
	rootDepth := entries[start].Depth
	for i := start + 1; i < len(entries); i++ {
		if entries[i].Depth <= rootDepth {
			end = i
			break
		}
	}

	playlist := entity.BookTOCPlaylist{
		BookID:    bookID,
		HeadingID: headingID,
		Lang:      lang,
		Items:     make([]entity.BookTOCPlaylistItem, 0),
	}

	for i := start; i < end; i++ {
		entry := entries[i]
		if entry.Audio == nil {
			playlist.MissingCount++
			continue
		}

		playlist.Items = append(playlist.Items, entity.BookTOCPlaylistItem{
			HeadingID:       entry.HeadingID,
			Title:           entry.Title,
			URL:             entry.Audio.URL,
			DurationSeconds: entry.Audio.DurationSeconds,
			Narrator:        entry.Audio.Narrator,
			MIMEType:        entry.Audio.MIMEType,
		})

		if entry.Audio.DurationSeconds != nil {
			playlist.TotalDurationSeconds += *entry.Audio.DurationSeconds
		}

		skipDepth := entry.Depth
		for i+1 < end && entries[i+1].Depth > skipDepth {
			i++
		}
	}

	return playlist, nil
}

func tocNodeFromEntry(entry entity.BookTOCEntry) entity.BookTOCNode {
	return entity.BookTOCNode{
		BookID:                entry.BookID,
		HeadingID:             entry.HeadingID,
		ParentID:              entry.ParentID,
		PageID:                entry.PageID,
		Depth:                 entry.Depth,
		Ordinal:               entry.Ordinal,
		Title:                 entry.Title,
		HasAudio:              entry.HasAudio,
		HasTranslation:        entry.HasTranslation,
		TranslationStatus:     entry.TranslationStatus,
		TranslationReviewedBy: entry.TranslationReviewedBy,
		TranslationReviewedAt: entry.TranslationReviewedAt,
		Audio:                 entry.Audio,
		Children:              []entity.BookTOCNode{},
	}
}

func tocLinkFromEntry(entry entity.BookTOCEntry) entity.BookTOCLink {
	return entity.BookTOCLink{
		HeadingID:             entry.HeadingID,
		Title:                 entry.Title,
		ParentID:              entry.ParentID,
		PageID:                entry.PageID,
		Depth:                 entry.Depth,
		Ordinal:               entry.Ordinal,
		HasAudio:              entry.HasAudio,
		HasTranslation:        entry.HasTranslation,
		TranslationStatus:     entry.TranslationStatus,
		TranslationReviewedBy: entry.TranslationReviewedBy,
		TranslationReviewedAt: entry.TranslationReviewedAt,
	}
}

func reverseLinks(links []entity.BookTOCLink) {
	for i, j := 0, len(links)-1; i < j; i, j = i+1, j-1 {
		links[i], links[j] = links[j], links[i]
	}
}
