package main

import "fmt"

type dop struct {
	kind byte // ' ', '-', '+'
	text string
}

// diffOps computes a line diff using a longest-common-subsequence table.
func diffOps(a, b []string) []dop {
	n, m := len(a), len(b)
	l := make([][]int, n+1)
	for i := range l {
		l[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				l[i][j] = l[i+1][j+1] + 1
			} else if l[i+1][j] >= l[i][j+1] {
				l[i][j] = l[i+1][j]
			} else {
				l[i][j] = l[i][j+1]
			}
		}
	}
	var ops []dop
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, dop{' ', a[i]})
			i++
			j++
		case l[i+1][j] >= l[i][j+1]:
			ops = append(ops, dop{'-', a[i]})
			i++
		default:
			ops = append(ops, dop{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, dop{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, dop{'+', b[j]})
	}
	return ops
}

func rng(start, length int) string {
	if length == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, length)
}

// unifiedDiff returns unified-diff lines (empty if the inputs are equal).
func unifiedDiff(a, b []string, from, to string) []string {
	ops := diffOps(a, b)
	const ctx = 3
	n := len(ops)
	aLine, bLine := make([]int, n+1), make([]int, n+1)
	for i, o := range ops {
		aLine[i+1], bLine[i+1] = aLine[i], bLine[i]
		if o.kind != '+' {
			aLine[i+1]++
		}
		if o.kind != '-' {
			bLine[i+1]++
		}
	}
	var out []string
	lastEnd, i := 0, 0
	for i < n {
		if ops[i].kind == ' ' {
			i++
			continue
		}
		start := i - ctx
		if start < lastEnd {
			start = lastEnd
		}
		end, j := i, i
		for j < n {
			if ops[j].kind != ' ' {
				end = j
				j++
				continue
			}
			k := j
			for k < n && ops[k].kind == ' ' {
				k++
			}
			if k < n && k-j <= 2*ctx {
				j = k
				continue
			}
			break
		}
		stop := end + 1 + ctx
		if stop > n {
			stop = n
		}
		if len(out) == 0 {
			out = append(out, "--- "+from, "+++ "+to)
		}
		aLen, bLen := aLine[stop]-aLine[start], bLine[stop]-bLine[start]
		aStart, bStart := aLine[start]+1, bLine[start]+1
		if aLen == 0 {
			aStart--
		}
		if bLen == 0 {
			bStart--
		}
		out = append(out, fmt.Sprintf("@@ -%s +%s @@", rng(aStart, aLen), rng(bStart, bLen)))
		for _, o := range ops[start:stop] {
			out = append(out, string(o.kind)+o.text)
		}
		lastEnd, i = stop, stop
	}
	return out
}
