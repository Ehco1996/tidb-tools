package dbutil

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/pingcap/errors"
	"github.com/pingcap/parser"
	"github.com/pingcap/parser/ast"
	"github.com/pingcap/parser/auth"
)

// ShowVersion queries variable 'version' and returns its value.
func ShowVersion(ctx context.Context, db *sql.DB) (value string, err error) {
	return ShowMySQLVariable(ctx, db, "version")
}

// ShowLogBin queries variable 'log_bin' and returns its value.
func ShowLogBin(ctx context.Context, db *sql.DB) (value string, err error) {
	return ShowMySQLVariable(ctx, db, "log_bin")
}

// ShowBinlogFormat queries variable 'binlog_format' and returns its value.
func ShowBinlogFormat(ctx context.Context, db *sql.DB) (value string, err error) {
	return ShowMySQLVariable(ctx, db, "binlog_format")
}

// ShowBinlogRowImage queries variable 'binlog_row_image' and returns its values.
func ShowBinlogRowImage(ctx context.Context, db *sql.DB) (value string, err error) {
	return ShowMySQLVariable(ctx, db, "binlog_row_image")
}

// ShowServerID queries variable 'server_id' and returns its value.
func ShowServerID(ctx context.Context, db *sql.DB) (serverID uint64, err error) {
	value, err := ShowMySQLVariable(ctx, db, "server_id")
	if err != nil {
		return 0, errors.Trace(err)
	}

	serverID, err = strconv.ParseUint(value, 10, 64)
	return serverID, errors.Annotatef(err, "parse server_id %s failed", value)
}

// ShowMySQLVariable queries MySQL variable and returns its value.
func ShowMySQLVariable(ctx context.Context, db *sql.DB, variable string) (value string, err error) {
	query := fmt.Sprintf("SHOW GLOBAL VARIABLES LIKE '%s';", variable)
	err = db.QueryRowContext(ctx, query).Scan(&variable, &value)
	if err != nil {
		return "", errors.Trace(err)
	}
	return value, nil
}

// ShowGrants queries privileges for a mysql user.
// For mysql 8.0, if user has granted roles, ShowGrants also extract privilege from roles.
func ShowGrants(ctx context.Context, db *sql.DB, user, host string) ([]string, error) {
	if host == "" {
		host = "%"
	}

	var query string
	if user == "" {
		// for current user.
		query = "SHOW GRANTS"
	} else {
		query = fmt.Sprintf("SHOW GRANTS FOR '%s'@'%s'", user, host)
	}

	readGrantsFunc := func() ([]string, error) {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return nil, errors.Trace(err)
		}
		defer rows.Close()

		grants := make([]string, 0, 8)
		for rows.Next() {
			var grant string
			err = rows.Scan(&grant)
			if err != nil {
				return nil, errors.Trace(err)
			}

			// TiDB parser does not support parse `IDENTIFIED BY PASSWORD <secret>`,
			// but it may appear in some cases, ref: https://dev.mysql.com/doc/refman/5.6/en/show-grants.html.
			// We do not need the password in grant statement, so we can replace it.
			grant = strings.Replace(grant, "IDENTIFIED BY PASSWORD <secret>", "IDENTIFIED BY PASSWORD 'secret'", 1)

			// support parse `IDENTIFIED BY PASSWORD WITH {GRANT OPTION | resource_option} ...`
			grant = strings.Replace(grant, "IDENTIFIED BY PASSWORD WITH", "IDENTIFIED BY PASSWORD 'secret' WITH", 1)

			// support parse `IDENTIFIED BY PASSWORD`
			if strings.HasSuffix(grant, "IDENTIFIED BY PASSWORD") {
				grant = grant + " 'secret'"
			}

			grants = append(grants, grant)
		}
		if rows.Err() != nil {
			return nil, errors.Trace(err)
		}
		return grants, nil
	}

	grants, err := readGrantsFunc()
	if err != nil {
		return nil, errors.Trace(err)
	}

	// for mysql 8.0, we should collect granted roles
	var roles []*auth.RoleIdentity
	p := parser.New()
	for _, grant := range grants {
		node, err := p.ParseOneStmt(grant, "", "")
		if err != nil {
			return nil, err
		}
		if grantRoleStmt, ok := node.(*ast.GrantRoleStmt); ok {
			roles = append(roles, grantRoleStmt.Roles...)
		}
	}

	if len(roles) == 0 {
		return grants, nil
	}

	var s strings.Builder
	s.WriteString(query)
	s.WriteString(" USING ")
	for i, role := range roles {
		if i > 0 {
			s.WriteString(", ")
		}
		s.WriteString(role.String())
	}
	query = s.String()

	return readGrantsFunc()
}
