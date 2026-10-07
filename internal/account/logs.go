package account

import (
	"api-manager/internal/model"
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Service) callLogs(w http.ResponseWriter, r *http.Request, userID string) {
	values := r.URL.Query()
	q := model.CallLogQuery{UserID: userID, Search: strings.TrimSpace(values.Get("search")), Method: strings.ToUpper(values.Get("method")), RequestID: values.Get("request_id"), StatusMin: 100, StatusMax: 599, Page: 1, PageSize: 20, From: time.Now().Add(-90 * 24 * time.Hour), To: time.Now().Add(time.Minute)}
	if userID == "" {
		q.UserID = values.Get("user_id")
	}
	if userID != "" && values.Get("user_id") != "" && values.Get("user_id") != userID {
		write(w, 403, map[string]string{"error": "只能查询自己的日志"})
		return
	}
	if len(q.Search) > 200 || len(q.RequestID) > 128 {
		write(w, 400, map[string]string{"error": "查询条件过长"})
		return
	}
	if q.Method != "" && !contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}, q.Method) {
		write(w, 400, map[string]string{"error": "请求方法无效"})
		return
	}
	for name, dest := range map[string]*int{"page": &q.Page, "page_size": &q.PageSize} {
		if raw := values.Get(name); raw != "" {
			v, e := strconv.Atoi(raw)
			if e != nil || v < 1 || v > 10000 || (name == "page_size" && v > 100) {
				write(w, 400, map[string]string{"error": "分页参数无效"})
				return
			}
			*dest = v
		}
	}
	if raw := values.Get("status"); raw != "" {
		if contains([]string{"2xx", "3xx", "4xx", "5xx"}, raw) {
			n := int(raw[0] - '0')
			q.StatusMin = n * 100
			q.StatusMax = n*100 + 99
		} else {
			n, e := strconv.Atoi(raw)
			if e != nil || n < 100 || n > 599 {
				write(w, 400, map[string]string{"error": "状态码无效"})
				return
			}
			q.StatusMin = n
			q.StatusMax = n
		}
	}
	for name, dest := range map[string]*time.Time{"from": &q.From, "to": &q.To} {
		if raw := values.Get(name); raw != "" {
			v, e := time.Parse(time.RFC3339, raw)
			if e != nil {
				write(w, 400, map[string]string{"error": "时间格式无效"})
				return
			}
			*dest = v
		}
	}
	if q.From.After(q.To) {
		write(w, 400, map[string]string{"error": "开始时间不能晚于结束时间"})
		return
	}
	st, ok := s.store.(interface {
		QueryCallLogs(context.Context, model.CallLogQuery) (model.CallLogPage, error)
	})
	if !ok {
		write(w, 503, map[string]string{"error": "日志查询不可用"})
		return
	}
	result, e := st.QueryCallLogs(r.Context(), q)
	if e != nil {
		write(w, 503, map[string]string{"error": "日志读取失败"})
		return
	}
	write(w, 200, result)
}
