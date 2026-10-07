package store

import (
	"api-manager/internal/model"
	"context"
)

func (p *Postgres) QueryCallLogs(ctx context.Context, q model.CallLogQuery) (model.CallLogPage, error) {
	if q.Page < 1 || q.Page > 10000 {
		q.Page = 1
	}
	if q.PageSize < 1 || q.PageSize > 100 {
		q.PageSize = 20
	}
	out := model.CallLogPage{Items: []model.CallLog{}, Page: q.Page, PageSize: q.PageSize}
	where := ` FROM user_call_logs l LEFT JOIN users u ON u.id=l.user_id WHERE ($1='' OR l.user_id::text=$1) AND ($2='' OR position(lower($2) in lower(l.api_name||' '||l.path||' '||COALESCE(u.username,'')||' '||l.client_ip))>0) AND ($3='' OR l.method=$3) AND l.status BETWEEN $4 AND $5 AND l.created_at>=$6 AND l.created_at<=$7 AND ($8='' OR l.request_id=$8)`
	args := []any{q.UserID, q.Search, q.Method, q.StatusMin, q.StatusMax, q.From, q.To, q.RequestID}
	if err := p.pool.QueryRow(ctx, `SELECT COUNT(*)`+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := p.pool.Query(ctx, `SELECT l.id,l.user_id,l.api_id,l.api_name,l.method,l.path,l.client_ip,l.status,l.price_micros,l.duration_ms,l.created_at,l.request_id,l.trace_id,l.credential_id,COALESCE(u.username,'')`+where+` ORDER BY l.created_at DESC,l.id DESC LIMIT $9 OFFSET $10`, append(args, q.PageSize, (q.Page-1)*q.PageSize)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v model.CallLog
		if err = rows.Scan(&v.ID, &v.UserID, &v.APIID, &v.APIName, &v.Method, &v.Path, &v.ClientIP, &v.Status, &v.PriceMicros, &v.DurationMS, &v.CreatedAt, &v.RequestID, &v.TraceID, &v.CredentialID, &v.Username); err != nil {
			return out, err
		}
		out.Items = append(out.Items, v)
	}
	return out, rows.Err()
}
