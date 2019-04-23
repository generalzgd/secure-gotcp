/**
 * @version: 1.0.0
 * @author: zhangguodong:general_zgd
 * @license: LGPL v3
 * @contact: general_zgd@163.com
 * @site: github.com/generalzgd
 * @software: GoLand
 * @file: conn_test.go
 * @time: 2018/9/7 10:57
 */
package gotcp

import "testing"

func TestSetLogDir(t *testing.T) {
	SetLogDir("log4")

	WriteLog("ttttttt")
}
