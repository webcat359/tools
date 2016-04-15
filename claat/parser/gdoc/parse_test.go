// Copyright 2016 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gdoc

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/googlecodelabs/tools/claat/render"
	"github.com/googlecodelabs/tools/claat/types"
)

func trimMarkupSpace(s string) string {
	var buf bytes.Buffer
	for _, l := range strings.Split(s, "\n") {
		buf.WriteString(strings.TrimSpace(l))
	}
	return buf.String()
}

func markupReader(s string) io.Reader {
	s = trimMarkupSpace(s)
	return strings.NewReader(s)
}

func TestStringSlice(t *testing.T) {
	tests := []struct {
		in  string
		out []string
	}{
		{"one", []string{"one"}},
		{" two ", []string{"two"}},
		{" one, two", []string{"one", "two"}},
		{" one, two and a half, three", []string{"one", "two and a half", "three"}},
	}
	for i, test := range tests {
		v := stringSlice(test.in)
		if !reflect.DeepEqual(v, test.out) {
			t.Errorf("%d: stringSlice(%q): %v; want %v", i, test.in, v, test.out)
		}
	}
}

func TestParseStepDuration(t *testing.T) {
	tests := []struct {
		markup string
		dur    time.Duration
	}{
		{`<p><span class="c9">Duration: 1:30</span></p>`, 2 * time.Minute},
		{`<p><span class="c9">Duration: 1:30 </span></p>`, 2 * time.Minute},
		{`<p><span class="c9">Duration: 1:30</span> </p>`, 2 * time.Minute},
		{`<p><span class="c9">Duration : 5</span></p>`, 5 * time.Minute},
		{`<p><span class="c9">duration: 1</span></p>`, time.Minute},
	}
	for i, test := range tests {
		doc, err := html.Parse(strings.NewReader(test.markup))
		if err != nil {
			t.Errorf("%d: Parse(%q): %v", i, test.markup, err)
		}
		ds := &docState{
			step: &types.Step{Content: types.NewListNode()},
			css:  cssStyle{".c9": {"color": metaColor}},
			cur:  doc.FirstChild,
		}
		parseTop(ds)
		if ds.step.Duration != test.dur {
			t.Errorf("%d: ds.step.Duration = %v; want %v", i, ds.step.Duration, test.dur)
		}
	}
}

func TestParseTopCodeBlock(t *testing.T) {
	const markup = `
	<table cellpadding="0" cellspacing="0"><tbody><tr>
	<td colspan="1" rowspan="1">
		<p><span class="code">start func() {<br>}</span><span class="code"></span></p>
		<p><span class="code"></span></p>
		<p><span class="code">func2() {<br>}</span><span class="code">&nbsp;// comment</span></p>
	</td>
	</tr></tbody></table>

	<table cellpadding="0" cellspacing="0"><tbody><tr>
	<td colspan="1" rowspan="1">
		<p><span class="term">adb shell am start -a VIEW \</span></p>
		<p><span class="term">-d &quot;http://host&quot; app</span></p>
	</td>
	</tr></tbody></table>
	`

	code := "start func() {\n}\n\nfunc2() {\n} // comment"
	term := "adb shell am start -a VIEW \\\n-d \"http://host\" app"
	content := types.NewListNode()
	content.Append(types.NewCodeNode(code, false))
	content.Append(types.NewCodeNode(term, true))

	doc, err := html.Parse(markupReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	ds := &docState{
		step: &types.Step{Content: types.NewListNode()},
		css: cssStyle{
			".code": {"font-family": fontCode},
			".term": {"font-family": fontConsole},
		},
		cur: doc.FirstChild,
	}
	parseTop(ds)

	html1, _ := render.HTML("", ds.step.Content)
	html2, _ := render.HTML("", content)
	s1 := strings.TrimSpace(string(html1))
	s2 := strings.TrimSpace(string(html2))
	if s1 != s2 {
		t.Errorf("step.Content:\n\n%s\nwant:\n\n%s", s1, s2)
	}
}

func TestParseDoc(t *testing.T) {
	const markup = `
	<html><head><style>
		.meta { color: #b7b7b7 }
		.code { font-family: "Courier New" }
		.term { font-family: "Consolas" }
		.btn { background-color: #6aa84f }
		.bold { font-weight: bold }
		.ita { font-style: italic }
		.nibox { background-color: #fce5cd }
		.survey { background-color: #cfe2f3 }
		.comment { border: 1px solid black }
	</style></head>
	<body>
		<p class="title"><a name="a1"></a><span>Test Codelab</span></p>

		<p>this should be ignored</p>

		<h1><a name="a2"></a><span>Overview</span></h1>
		<p><span class="meta">Duration: 1:00</span></p>

		<img src="https://host/image.png">
		<p><img src="https://host/small.png" style="height: 10px; width: 25.5px"> icon.</p>

		<h3><a name="a3"></a><span>What you&rsquo;ll learn</span></h3>
		<ul class="start">
		<li><span>First </span><span>One</span><sup><a href="#cmnt1" name="cmnt_ref1" target="_blank">[a]</a></sup></li>
		<li><span>Two </span><span><a href="https://google.com/url?q=http%3A%2F%2Fexample.com">Link</a></span></li>
		</ul>
		<ul><li><span>Three</span></li></ul>

		<p>This is<span class="code"> code</span>.</p>
		<p>Just <span>a</span> paragraph.</p>
		<p><a href="url">one</a><a href="url"> url</a></p>
		<p><span class="btn"><a href="http://example.com">Download Zip</a></span></p>
		<p>
			<span class="bold">Bo</span><span>&nbsp;</span><span class="bold">ld</span>
			<span class="ita"> italic</span> text <span class="bold ita">or both.</span></p>

		<h3><a href="http://host/file.java">a file</a></h3>
		<table cellpadding="0" cellspacing="0"><tbody><tr>
		<td colspan="1" rowspan="1">
			<p><span class="code">start func() {<br>}</span></p>
			<p><span class="code"></span></p>
			<p><span class="code">func2() {<br>}</span><span class="code">&nbsp;// comment</span></p>
		</td>
		</tr></tbody></table>

		<table cellpadding="0" cellspacing="0"><tbody><tr>
		<td colspan="1" rowspan="1">
			<p><span class="term">adb shell am start -a VIEW \</span></p>
			<p><span class="term">-d &quot;http://host&quot; app</span></p>
		</td>
		</tr></tbody></table>

		<table cellpadding="0" cellspacing="0"><tbody><tr>
		<td class="nibox" colspan="1" rowspan="1">
			<p><span class="bold">warning</span></p>
			<p><span>negative box.</span></p>
		</td>
		</tr></tbody></table>

		<table cellpadding="0" cellspacing="0"><tbody><tr>
		<td class="survey" colspan="1" rowspan="1">
		<h4><a name="x"></a><span class="code">How</span><span class="ita">&nbsp;will you use it?</span></h4>
		<ul><li class="bold"><span class="c5">Read it</span></li></ul>
		<ul><li class="c23 c47"><span class="c5">Read and complete</span></li></ul>
		<p class="c23 c44"><span class="c5"></span></p>
		<h4><a name="asd"></a><span>How</span><span>&nbsp;would you rate?</span></h4>
		<ul>
			<li class="c19 c47"><span class="c5">Novice</span></li>
			<li class="c19 c47"><span class="c5">Intermediate</span></li>
			<li class="c19 c47"><span class="c5">Proficient</span></li>
		</ul>
		<p class="c23 c44"><span class="c5"></span></p>
		</td>
		</tr></tbody></table>
		<div class="comment">
		<p><a href="#cmnt_ref1" name="cmnt1">[a]</a><span class="c16 c8">Test comment.</span></p>
		</div>
	</body>
	</html>
	`
	c, err := Parse(markupReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	if c.Meta.Title != "Test Codelab" {
		t.Errorf("c.Meta.Title = %q; want Test Codelab", c.Meta.Title)
	}
	if c.Meta.ID != "test-codelab" {
		t.Errorf("c.ID = %q; want test-codelab", c.Meta.ID)
	}
	if len(c.Steps) == 0 {
		t.Fatalf("len(c.Steps) = 0")
	}
	step := c.Steps[0]
	if step.Title != "Overview" {
		t.Errorf("step.Title = %q; want Overview", step.Title)
	}

	content := types.NewListNode()

	img := types.NewImageNode("https://host/image.png")
	para := types.NewListNode(img)
	para.MutateBlock(true)
	content.Append(para)

	img = types.NewImageNode("https://host/small.png")
	img.MaxWidth = 25.5
	para = types.NewListNode(img, types.NewTextNode(" icon."))
	para.MutateBlock(true)
	content.Append(para)

	h := types.NewHeaderNode(3, types.NewTextNode("What you'll learn"))
	h.MutateType(types.NodeHeaderCheck)
	content.Append(h)
	list := types.NewItemsListNode("", 0)
	list.MutateType(types.NodeItemsCheck)
	list.NewItem().Append(types.NewTextNode("First One"))
	item := list.NewItem()
	item.Append(types.NewTextNode("Two "))
	item.Append(types.NewURLNode("http://example.com", types.NewTextNode("Link")))
	list.NewItem().Append(types.NewTextNode("Three"))
	content.Append(list)

	para = types.NewListNode()
	para.MutateBlock(true)
	para.Append(types.NewTextNode("This is "))
	txt := types.NewTextNode("code")
	txt.Code = true
	para.Append(txt)
	para.Append(types.NewTextNode("."))
	content.Append(para)

	para = types.NewListNode()
	para.MutateBlock(true)
	para.Append(types.NewTextNode("Just a paragraph."))
	content.Append(para)

	u := types.NewURLNode("url", types.NewTextNode("one url"))
	para = types.NewListNode(u)
	para.MutateBlock(true)
	content.Append(para)

	btn := types.NewButtonNode(true, true, true, types.NewTextNode("Download Zip"))
	dl := types.NewURLNode("http://example.com", btn)
	para = types.NewListNode(dl)
	para.MutateBlock(true)
	content.Append(para)

	b := types.NewTextNode("Bo ld")
	b.Bold = true
	i := types.NewTextNode(" italic")
	i.Italic = true
	bi := types.NewTextNode("or both.")
	bi.Bold = true
	bi.Italic = true
	para = types.NewListNode(b, i, types.NewTextNode(" text "), bi)
	para.MutateBlock(true)
	content.Append(para)

	h = types.NewHeaderNode(3, types.NewURLNode(
		"http://host/file.java", types.NewTextNode("a file")))
	content.Append(h)

	code := "start func() {\n}\n\nfunc2() {\n} // comment"
	cn := types.NewCodeNode(code, false)
	cn.MutateBlock(1)
	content.Append(cn)

	term := "adb shell am start -a VIEW \\\n-d \"http://host\" app"
	tn := types.NewCodeNode(term, true)
	tn.MutateBlock(2)
	content.Append(tn)

	b = types.NewTextNode("warning")
	b.Bold = true
	n1 := types.NewListNode(b)
	n1.MutateBlock(true)
	n2 := types.NewListNode(types.NewTextNode("negative box."))
	n2.MutateBlock(true)
	box := types.NewInfoboxNode(types.InfoboxNegative, n1, n2)
	content.Append(box)

	sv := types.NewSurveyNode("test-codelab-1")
	sv.Groups = append(sv.Groups, &types.SurveyGroup{
		Name:    "How will you use it?",
		Options: []string{"Read it", "Read and complete"},
	})
	sv.Groups = append(sv.Groups, &types.SurveyGroup{
		Name:    "How would you rate?",
		Options: []string{"Novice", "Intermediate", "Proficient"},
	})
	content.Append(sv)

	html1, _ := render.HTML("", step.Content)
	html2, _ := render.HTML("", content)
	if html1 != html2 {
		t.Errorf("step.Content:\n\n%s\nwant:\n\n%s", html1, html2)
	}
}
