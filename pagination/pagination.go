package pagination

import (
	"net/http"
	"net/url"
	"strconv"
)

const DefaultLimit = 20
const DefaultWindow = 5

type View struct {
	CurrentPage int
	TotalPages  int
	HasPrev     bool
	PrevPage    int
	PrevURL     string
	HasNext     bool
	NextPage    int
	NextURL     string
	Pages       []Page
}

type Page struct {
	Number    int
	URL       string
	IsCurrent bool
}

func PageFromRequest(r *http.Request) int {
	if r == nil || r.URL == nil {
		return 1
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		return 1
	}
	return page
}

func Offset(page, limit int) int {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = DefaultLimit
	}
	return (page - 1) * limit
}

func TotalPages(totalItems, limit int) int {
	if totalItems < 1 {
		return 0
	}
	if limit < 1 {
		limit = DefaultLimit
	}
	return (totalItems + limit - 1) / limit
}

func BuildFromTotal(r *http.Request, totalItems, limit int) *View {
	return Build(r, PageFromRequest(r), TotalPages(totalItems, limit))
}

func Build(r *http.Request, currentPage, totalPages int) *View {
	return BuildWithWindow(r, currentPage, totalPages, DefaultWindow)
}

func BuildWithWindow(r *http.Request, currentPage, totalPages, windowSize int) *View {
	if totalPages < 1 {
		return nil
	}
	if windowSize < 1 {
		windowSize = DefaultWindow
	}
	if currentPage < 1 {
		currentPage = 1
	}
	if currentPage > totalPages {
		currentPage = totalPages
	}

	pagination := &View{
		CurrentPage: currentPage,
		TotalPages:  totalPages,
		HasPrev:     currentPage > 1,
		PrevPage:    currentPage - 1,
		HasNext:     currentPage < totalPages,
		NextPage:    currentPage + 1,
	}

	if pagination.HasPrev {
		pagination.PrevURL = pageURL(r, pagination.PrevPage)
	}
	if pagination.HasNext {
		pagination.NextURL = pageURL(r, pagination.NextPage)
	}

	start, end := visibleWindow(currentPage, totalPages, windowSize)
	pagination.Pages = make([]Page, 0, end-start+1)
	for page := start; page <= end; page++ {
		pagination.Pages = append(pagination.Pages, Page{
			Number:    page,
			URL:       pageURL(r, page),
			IsCurrent: page == currentPage,
		})
	}

	return pagination
}

func visibleWindow(currentPage, totalPages, windowSize int) (int, int) {
	if totalPages <= windowSize {
		return 1, totalPages
	}

	start := currentPage - windowSize/2
	end := start + windowSize - 1

	if start < 1 {
		start = 1
		end = windowSize
	}
	if end > totalPages {
		end = totalPages
		start = totalPages - windowSize + 1
	}

	return start, end
}

func pageURL(r *http.Request, page int) string {
	values := url.Values{}
	if r != nil && r.URL != nil {
		for key, existingValues := range r.URL.Query() {
			for _, existingValue := range existingValues {
				values.Add(key, existingValue)
			}
		}
	}
	values.Set("page", strconv.Itoa(page))
	return "?" + values.Encode()
}
