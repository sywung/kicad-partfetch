// Package search implements the LCSC global search flow.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
)

const pageSize = 25

type Item struct {
	PartID, MPN, Manufacturer, Package, Description string
	Stock                                           int
	Price, ImageURL, DatasheetURL                   string
	Source, URL, License                            string
	Stars                                           int
}
type Result struct {
	Items []Item
	Total int
	Page  int
}
type Client interface {
	Search(context.Context, string, int) (Result, error)
}
type Options struct {
	HTTPClient *http.Client
	Transport  http.RoundTripper
	Now        func() time.Time
	Sleep      func(context.Context, time.Duration) error
}
type HTTPClient struct {
	http      *http.Client
	now       func() time.Time
	sleep     func(context.Context, time.Duration) error
	mu        sync.Mutex
	last      time.Time
	requestMu sync.Mutex
}

func IsNotFound(err error) bool {
	var status *httpStatusError
	if !errors.As(err, &status) {
		return false
	}
	return status.status == http.StatusNotFound
}

func IsRateLimited(err error) bool {
	var status *httpStatusError
	if errors.As(err, &status) {
		return status.status == http.StatusForbidden || status.status == http.StatusTooManyRequests
	}
	return false
}

func New(o Options) *HTTPClient {
	c := o.HTTPClient
	if c == nil {
		tr := o.Transport
		if tr == nil {
			tr = http.DefaultTransport
		}
		c = &http.Client{Transport: tr, Timeout: 20 * time.Second}
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	sleep := o.Sleep
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	return &HTTPClient{http: c, now: now, sleep: sleep}
}

func (c *HTTPClient) Search(ctx context.Context, query string, page int) (Result, error) {
	if page < 1 {
		page = 1
	}
	global, err := c.post(ctx, "https://wmsc.lcsc.com/ftps/wm/search/v3/global", map[string]any{"keyword": query, "currentPage": 1, "pageSize": pageSize})
	if err != nil {
		return Result{}, err
	}
	var root globalResponse
	if err = json.Unmarshal(global, &root); err != nil {
		return Result{}, fmt.Errorf("decode LCSC search response: %w", err)
	}
	if root.Code != 200 {
		return Result{}, fmt.Errorf("LCSC search returned code %d", root.Code)
	}
	res := root.Result
	if len(res.Exact) > 0 {
		return Result{Items: convert(res.Exact), Total: res.Total, Page: page}, nil
	}
	if res.Products != nil && len(res.Products.List) > 0 {
		return Result{Items: convert(res.Products.List), Total: res.Total, Page: page}, nil
	}
	categories := leafCategories(res.Categories, res.TopResults)
	if len(categories) == 0 {
		return Result{Items: []Item{}, Total: 0, Page: page}, nil
	}
	body := map[string]any{
		"keyword": "", "globalKeyword": query, "catalogIdList": categories, "brandIdList": []int{}, "encapValueList": []string{},
		"isStock": false, "isOtherSuppliers": false, "isAsianBrand": false, "isDeals": false, "isRohsCert": false, "paramNameValueMap": map[string]any{},
		"currentPage": page, "pageSize": pageSize,
	}
	if res.Scene == "FULL_MATCH" {
		body["scene"] = "FULL_MATCH"
	}
	raw, err := c.post(ctx, "https://wmsc.lcsc.com/ftps/wm/product/query/list", body)
	if err != nil {
		return Result{}, err
	}
	var listing listResponse
	if err = json.Unmarshal(raw, &listing); err != nil {
		return Result{}, fmt.Errorf("decode LCSC product response: %w", err)
	}
	if listing.Code != 200 {
		return Result{}, fmt.Errorf("LCSC product query returned code %d", listing.Code)
	}
	return Result{Items: convert(listing.Result.Data), Total: listing.Result.Total, Page: page}, nil
}

func (c *HTTPClient) Community(ctx context.Context, query string, page int) (Result, error) {
	if page < 1 {
		page = 1
	}
	form := url.Values{"type": {"3"}, "doctype[]": {"2"}, "returnListStyle": {"classifyarr"}, "uid": {"user"}, "wd": {query}, "page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(pageSize)}}
	body, err := c.postForm(ctx, "https://easyeda.com/api/components/search", form)
	if err != nil {
		var status *httpStatusError
		if errors.As(err, &status) && (status.status == http.StatusForbidden || status.status == http.StatusTooManyRequests) {
			return Result{}, fmt.Errorf("%w: %v", easyeda.ErrRateLimited, err)
		}
		return Result{}, err
	}
	return decodeCommunity(body, page)
}

func decodeCommunity(body []byte, page int) (Result, error) {
	var response communityResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return Result{}, fmt.Errorf("decode EasyEDA community search response: %w", err)
	}
	items := make([]Item, 0, len(response.Result.Lists.User))
	for _, product := range response.Result.Lists.User {
		manufacturer := product.Owner.Nickname
		if manufacturer == "" {
			manufacturer = product.Owner.Username
		}
		image := product.Thumb
		if strings.HasPrefix(image, "//") {
			image = "https:" + image
		}
		items = append(items, Item{PartID: "easyeda:" + strings.ToLower(product.UUID), MPN: product.Title, Manufacturer: manufacturer, Package: product.DataStr.Head.CPara.Package, Description: product.Description, ImageURL: image, Source: "easyeda"})
	}
	return Result{Items: items, Total: communityTotal(response.Result.Facets.User), Page: page}, nil
}

func communityTotal(raw json.RawMessage) int {
	var total int
	if json.Unmarshal(raw, &total) == nil {
		return total
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		total, _ = strconv.Atoi(text)
	}
	return total
}

func (c *HTTPClient) postForm(ctx context.Context, target string, form url.Values) ([]byte, error) {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	c.mu.Lock()
	wait := time.Duration(0)
	if !c.last.IsZero() && c.now().Sub(c.last) < 1200*time.Millisecond {
		wait = 1200*time.Millisecond - c.now().Sub(c.last)
	}
	c.mu.Unlock()
	if wait > 0 {
		if err := c.sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.mu.Lock()
	c.last = c.now()
	c.mu.Unlock()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &httpStatusError{status: resp.StatusCode}
	}
	return body, nil
}

type httpStatusError struct{ status int }

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("HTTP request failed with status %d", e.status)
}

type communityResponse struct {
	Result struct {
		Lists struct {
			User []communityProduct `json:"user"`
		} `json:"lists"`
		Facets struct {
			User json.RawMessage `json:"user"`
		} `json:"facets"`
	} `json:"result"`
}

type communityProduct struct {
	UUID        string `json:"uuid"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Thumb       string `json:"thumb"`
	Owner       struct {
		Nickname string `json:"nickname"`
		Username string `json:"username"`
	} `json:"owner"`
	DataStr struct {
		Head struct {
			CPara struct {
				Package string `json:"package"`
			} `json:"c_para"`
		} `json:"head"`
	} `json:"dataStr"`
}

func (c *HTTPClient) post(ctx context.Context, url string, body any) ([]byte, error) {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	c.mu.Lock()
	wait := time.Duration(0)
	if !c.last.IsZero() && c.now().Sub(c.last) < time.Second {
		wait = time.Second - c.now().Sub(c.last)
	}
	c.mu.Unlock()
	if wait > 0 {
		if err := c.sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://www.lcsc.com")
	req.Header.Set("Referer", "https://www.lcsc.com/")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	c.mu.Lock()
	c.last = c.now()
	c.mu.Unlock()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &httpStatusError{status: resp.StatusCode}
	}
	return data, nil
}

type globalResponse struct {
	Code   int `json:"code"`
	Result struct {
		Total    int       `json:"totalCount"`
		Scene    string    `json:"scene"`
		Exact    []product `json:"exactMatchResult"`
		Products *struct {
			List []product `json:"productList"`
		} `json:"productSearchResultVO"`
		Categories []category `json:"catalogVOS"`
		TopResults []category `json:"topResults"`
	} `json:"result"`
}
type category struct {
	ID          int        `json:"catalogId"`
	Count       int        `json:"productNum"`
	Level       int        `json:"level"`
	Children    []category `json:"childCatelogs"`
	ChildrenAlt []category `json:"childCatalogs"`
}
type listResponse struct {
	Code   int `json:"code"`
	Result struct {
		Data  []product `json:"dataList"`
		Total int       `json:"totalRow"`
	} `json:"result"`
}
type product struct {
	Code        string `json:"productCode"`
	Model       string `json:"productModel"`
	Brand       string `json:"brandNameEn"`
	Package     string `json:"encapStandard"`
	Description string `json:"productIntroEn"`
	Stock       int    `json:"stockNumber"`
	Image       string `json:"productImageUrl"`
	PDF         string `json:"pdfUrl"`
	Prices      []struct {
		Ladder int         `json:"ladder"`
		USD    json.Number `json:"usdPrice"`
	} `json:"productPriceList"`
}

func convert(src []product) []Item {
	out := make([]Item, 0, len(src))
	for _, p := range src {
		price := ""
		if len(p.Prices) > 0 {
			lowest := p.Prices[0]
			for _, candidate := range p.Prices[1:] {
				if candidate.Ladder < lowest.Ladder {
					lowest = candidate
				}
			}
			if v, err := strconv.ParseFloat(string(lowest.USD), 64); err == nil {
				price = fmt.Sprintf("$%.4f", v)
			}
		}
		out = append(out, Item{PartID: p.Code, MPN: p.Model, Manufacturer: p.Brand, Package: p.Package, Description: p.Description, Stock: p.Stock, Price: price, ImageURL: p.Image, DatasheetURL: p.PDF, Source: "lcsc"})
	}
	return out
}

// leafCategories returns every category without children, in first-seen order.
// A parent category ID makes the product query return zero rows, so only leaves are usable.
func leafCategories(groups ...[]category) []int {
	ids := []int{}
	seen := map[int]bool{}
	var walk func(category)
	walk = func(c category) {
		children := append(append([]category{}, c.Children...), c.ChildrenAlt...)
		if len(children) == 0 {
			if c.ID != 0 && !seen[c.ID] {
				seen[c.ID] = true
				ids = append(ids, c.ID)
			}
			return
		}
		for _, child := range children {
			walk(child)
		}
	}
	for _, group := range groups {
		for _, c := range group {
			walk(c)
		}
	}
	return ids
}
