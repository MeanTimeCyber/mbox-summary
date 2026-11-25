// Package report renders mailbox summaries as standalone HTML documents.
package report

import (
	"encoding/json"
	"html/template"
	"os"
	"path/filepath"
	"time"

	"github.com/meantimecyber/mbox-summary/internal/model"
)

// pageData contains fields safely interpolated into the HTML template.
type pageData struct {
	Title           string
	Cutoff          string
	GeneratedAt     string
	MessageCount    int
	OwnerIdentities string
	WarningCount    int
	WeeklyJSON      template.JS
	YearlyJSON      template.JS
	SendersJSON     template.JS
	RecipientsJSON  template.JS
	WarningsJSON    template.JS
	SourcesJSON     template.JS
	DedupeJSON      template.JS
	FolderStatsJSON template.JS
	ThreadsJSON     template.JS
	AttachmentsJSON template.JS
}

// WriteFile writes a complete, self-contained HTML report to outputPath.
func WriteFile(outputPath string, summary model.Summary) error {
	data := pageData{
		Title:           "Mailbox Summary",
		Cutoff:          summary.Cutoff.UTC().Format("2006-01-02"),
		GeneratedAt:     summary.GeneratedAt.UTC().Format(time.RFC3339),
		MessageCount:    summary.MessageCount,
		OwnerIdentities: join(summary.OwnerIdentities),
		WarningCount:    len(summary.ParseWarnings),
		WeeklyJSON:      mustJSON(summary.Weekly),
		YearlyJSON:      mustJSON(summary.Yearly),
		SendersJSON:     mustJSON(summary.TopSenders),
		RecipientsJSON:  mustJSON(summary.TopRecipients),
		WarningsJSON:    mustJSON(summary.ParseWarnings),
		SourcesJSON:     mustJSON(summary.Sources),
		DedupeJSON:      mustJSON(summary.Dedupe),
		FolderStatsJSON: mustJSON(summary.FolderStats),
		ThreadsJSON:     mustJSON(summary.TopThreads),
		AttachmentsJSON: mustJSON(summary.Attachments),
	}

	directory := filepath.Dir(outputPath)
	file, err := os.CreateTemp(directory, ".mbox-summary-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	cleanup := true
	defer func() {
		if cleanup {
			file.Close()
			os.Remove(tempPath)
		}
	}()

	if err := file.Chmod(0o644); err != nil {
		return err
	}
	if err := htmlTemplate().Execute(file, data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, outputPath); err != nil {
		return err
	}

	cleanup = false
	return nil
}

// join joins owner addresses into display text.
func join(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	result := ""
	for index, value := range values {
		if index > 0 {
			result += ", "
		}
		result += value
	}
	return result
}

// mustJSON returns JSON embedded into the script; html/template escaping is intentionally disabled for script data.
func mustJSON(value any) template.JS {
	data, err := json.Marshal(value)
	if err != nil {
		return template.JS("[]")
	}
	if string(data) == "null" {
		return template.JS("[]")
	}
	return template.JS(data)
}

// htmlTemplate builds the standalone HTML template with embedded CSS and JavaScript.
func htmlTemplate() *template.Template {
	return template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root{color-scheme:light dark;--bg:#f6f7fb;--panel:#fff;--text:#1d2433;--muted:#667085;--blue:#2563eb;--green:#16a34a}*{box-sizing:border-box}body{margin:0;font:15px/1.5 system-ui,sans-serif;background:var(--bg);color:var(--text)}main{max-width:1200px;margin:auto;padding:24px}.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:12px}.card,.panel{background:var(--panel);border:1px solid #d0d5dd;border-radius:10px;padding:16px;margin:12px 0}.card strong{display:block;font-size:28px}.table-wrap{width:100%;max-width:100%;overflow-x:auto}.table-wrap table{border-collapse:collapse;width:100%;min-width:760px}th,td{border-bottom:1px solid #e4e7ec;text-align:left;padding:8px;vertical-align:top}.thread-participants{max-width:280px;overflow-wrap:anywhere}.chart{width:100%;height:300px;border:1px solid #e4e7ec;border-radius:8px;overflow-x:auto}.chart svg{display:block;min-width:640px;width:100%;height:300px}.chart text{font:11px system-ui,sans-serif;fill:var(--muted)}.split{display:grid;grid-template-columns:1fr 1fr;gap:16px}.tag{display:inline-block;border-radius:12px;padding:2px 8px;background:#e8eefc;color:#1d4ed8}.warning{color:#b42318}@media(max-width:800px){.split{grid-template-columns:1fr}}@media print{body{background:#fff}.panel,.card{break-inside:avoid}.table-wrap{overflow:visible}}
</style>
</head>
<body><main>
<h1>{{.Title}}</h1>
<p>Cutoff: {{.Cutoff}} · Generated: {{.GeneratedAt}}</p>
<nav><a href="#yearly">Yearly</a> · <a href="#weekly">Weekly</a> · <a href="#threads">Threads</a> · <a href="#attachments">Attachments</a> · <a href="#sources">Sources</a> · <a href="#senders">Senders</a> · <a href="#recipients">Recipients</a></nav>
<section class="cards">
<div class="card">Messages<strong>{{.MessageCount}}</strong></div>
<div class="card">Mailbox identities<strong style="font-size:16px">{{.OwnerIdentities}}</strong></div>
<div class="card">Parse warnings<strong>{{.WarningCount}}</strong></div>
</section>
<section id="yearly" class="panel"><h2>Yearly volume</h2><div id="yearlyChart" class="chart" role="img" aria-label="Yearly sent and received email volume"></div><div id="yearlyTable"></div></section>
<section id="weekly" class="panel"><h2>Weekly volume</h2><div id="weeklyChart" class="chart" role="img" aria-label="Weekly sent and received email volume"></div><div id="weeklyTable"></div></section>
<section id="threads" class="panel"><h2>Top 20 conversation threads</h2><div id="threadTable"></div></section>
<section id="attachments" class="panel"><h2>Sent attachments</h2><div id="attachmentTable"></div></section>
<section id="sources" class="panel"><h2>Source folders and files</h2><div id="sourceTable"></div><div id="folderTable"></div><div id="dedupeStats"></div></section>
<div class="split">
<section id="senders" class="panel"><h2>Top senders</h2><div id="senderTable"></div></section>
<section id="recipients" class="panel"><h2>Top recipients</h2><div id="recipientTable"></div></section>
</div>
<section class="panel"><h2>Parse warnings</h2><div id="warnings"></div></section>
</main>
<script>
const weekly={{.WeeklyJSON}};
const yearly={{.YearlyJSON}};
const senders={{.SendersJSON}};
const recipients={{.RecipientsJSON}};
const warnings={{.WarningsJSON}};
const sources={{.SourcesJSON}};
const dedupe={{.DedupeJSON}};
const folderStats={{.FolderStatsJSON}};
const threads={{.ThreadsJSON}};
const attachments={{.AttachmentsJSON}};
// drawChart renders a dependency-free SVG chart for all browsers and print output.
function drawChart(container, rows, labelKey) {
 if(!rows.length){container.textContent='No data.';return}
 const max=Math.max(1,...rows.map(r=>Math.max(r.Sent,r.Received)));
 const width=960,height=300,left=48,right=12,top=18,bottom=36;
 const plotWidth=width-left-right,plotHeight=height-top-bottom;
 const x=index=>left+(rows.length===1?plotWidth/2:index*plotWidth/(rows.length-1));
 const y=value=>top+plotHeight-(value/max)*plotHeight;

 let svg='<svg viewBox="0 0 '+width+' '+height+'" preserveAspectRatio="xMidYMid meet" aria-label="'+escapeText(labelKey)+' sent and received volume">';
 svg+='<rect x="0" y="0" width="'+width+'" height="'+height+'" fill="none" stroke="#d0d5dd"/>';
 svg+='<g stroke="#e4e7ec" stroke-width="1">';
 for(let step=0;step<=4;step++){
  const yGrid=top+plotHeight*step/4;
  const gridValue=Math.round(max*(4-step)/4);
  svg+='<line x1="'+left+'" y1="'+yGrid+'" x2="'+(width-right)+'" y2="'+yGrid+'"/>';
  svg+='<text x="'+(left-8)+'" y="'+(yGrid+4)+'" text-anchor="end">'+gridValue+'</text>';
 }
 svg+='</g>';
 for(const series of [{key:'Sent',color:'#2563eb'},{key:'Received',color:'#16a34a'}]){
  let points=[];
  rows.forEach((r,index)=>points.push(x(index)+','+y(r[series.key])));
  svg+='<polyline fill="none" stroke="'+series.color+'" stroke-width="2.5" stroke-linejoin="round" stroke-linecap="round" points="'+points.join(' ')+'"/>';
  rows.forEach((r,index)=>{
   const cx=x(index),cy=y(r[series.key]);
   svg+='<circle cx="'+cx+'" cy="'+cy+'" r="3" fill="'+series.color+'"><title>'+escapeText(r[labelKey])+': '+r[series.key]+' '+series.key.toLowerCase()+'</title></circle>';
  });
 }
 const labelCount=rows.length<=12?rows.length:12;
 for(let i=0;i<labelCount;i++){
  const rowIndex=labelCount===1?0:Math.round(i*(rows.length-1)/(labelCount-1));
  const row=rows[rowIndex];
  svg+='<text x="'+x(rowIndex)+'" y="'+(height-12)+'" text-anchor="middle">'+escapeText(row[labelKey])+'</text>';
 }
 svg+='<line x1="'+left+'" y1="'+(top-4)+'" x2="'+(left+14)+'" y2="'+(top-4)+'" stroke="#2563eb" stroke-width="3"/>';
 svg+='<text x="'+(left+18)+'" y="'+(top)+'">Sent</text>';
 svg+='<line x1="'+(left+88)+'" y1="'+(top-4)+'" x2="'+(left+102)+'" y2="'+(top-4)+'" stroke="#16a34a" stroke-width="3"/>';
 svg+='<text x="'+(left+106)+'" y="'+(top)+'">Received</text>';
 svg+='</svg>';
 container.innerHTML=svg;
}
function periodTable(rows){
 if(!rows.length)return '<p>No data.</p>';
 let html='<table><thead><tr><th>Period</th><th>Sent</th><th>Received</th></tr></thead><tbody>';
 rows.forEach(r=>html+='<tr><td>'+escapeText(r.Period)+'</td><td>'+r.Sent+'</td><td>'+r.Received+'</td></tr>');return html+'</tbody></table>';
}
function rankTable(rows){
 if(!rows.length)return '<p>No data.</p>';
 let html='<table><thead><tr><th>Name/address</th><th>Count</th><th>Share</th><th>Kind</th></tr></thead><tbody>';
 rows.forEach(r=>{let address=r.Name?escapeText(r.Name)+'<br><code>'+escapeText(r.Address)+'</code>':'<code>'+escapeText(r.Address)+'</code>';html+='<tr><td>'+address+'</td><td>'+r.Count+'</td><td>'+r.Percentage.toFixed(1)+'%</td><td>'+ (r.MailingList?' <span class="tag">Mailing list</span>':'Personal')+'</td></tr>'});return html+'</tbody></table>';
}
function sourceTable(rows){
 if(!rows.length)return '<p>No source files.</p>';
 let html='<table><thead><tr><th>Folder</th><th>File</th><th>Messages</th><th>Duplicates</th><th>Warnings</th></tr></thead><tbody>';
 rows.forEach(r=>html+='<tr><td>'+escapeText(r.Folder)+'</td><td><code>'+escapeText(r.Path)+'</code></td><td>'+r.Messages+'</td><td>'+r.Duplicates+'</td><td>'+r.Warnings+'</td></tr>');
 return html+'</tbody></table>';
}
function folderTable(rows){
 if(!rows.length)return '';
 let html='<h3>Folder totals</h3><table><thead><tr><th>Folder</th><th>Messages</th><th>Sent</th><th>Received</th></tr></thead><tbody>';
 rows.forEach(r=>html+='<tr><td>'+escapeText(r.Folder)+'</td><td>'+r.Messages+'</td><td>'+r.Sent+'</td><td>'+r.Received+'</td></tr>');
 return html+'</tbody></table>';
}
function threadTable(rows){
 if(!rows.length)return '<p>No in-window conversation threads.</p>';
 let html='<div class="table-wrap"><table><thead><tr><th>Rank</th><th>Conversation</th><th>Messages</th><th>Participants</th><th>First message</th><th>Last message</th></tr></thead><tbody>';
 rows.forEach((r,index)=>{
  let subject=r.Subject?escapeText(r.Subject):'(No subject)';
  let participants=(r.Participants||[]).map(p=>p.Name?escapeText(p.Name)+' &lt;'+escapeText(p.Address)+'&gt;':'<code>'+escapeText(p.Address)+'</code>').join(', ');
  if(r.ParticipantCount>(r.Participants||[]).length) participants+=(participants?' · ':'')+'+'+(r.ParticipantCount-r.Participants.length)+' more';
  html+='<tr><td>'+ (index+1)+'</td><td><strong>'+subject+'</strong>'+(r.MailingList?' <span class="tag">Mailing list</span>':'')+'</td><td>'+r.MessageCount+'</td><td class="thread-participants">'+participants+'</td><td>'+escapeText((r.FirstSeen||'').slice(0,10))+'</td><td>'+escapeText((r.LastSeen||'').slice(0,10))+'</td></tr>';
 });
 return html+'</tbody></table></div>';
}
function formatBytes(bytes){
 if(bytes<1024)return bytes+' B';
 if(bytes<1024*1024)return (bytes/1024).toFixed(1)+' KiB';
 if(bytes<1024*1024*1024)return (bytes/(1024*1024)).toFixed(1)+' MiB';
 return (bytes/(1024*1024*1024)).toFixed(1)+' GiB';
}
function attachmentTable(stats){
 let html='<p><strong>Exported files:</strong> '+stats.ExportedFiles+' · <strong>Exported size:</strong> '+formatBytes(stats.ExportedBytes||0)+'</p>';
 if(!stats.TopSentTypes||!stats.TopSentTypes.length)return html+'<p>No sent attachments found.</p>';
 html+='<div class="table-wrap"><table><thead><tr><th>Rank</th><th>File type</th><th>Attachments</th><th>Total size</th></tr></thead><tbody>';
 stats.TopSentTypes.forEach((row,index)=>html+='<tr><td>'+ (index+1)+'</td><td><code>'+escapeText(row.FileType)+'</code></td><td>'+row.Count+'</td><td>'+formatBytes(row.SizeBytes||0)+'</td></tr>');
 return html+'</tbody></table></div>';
}
function escapeText(value){const el=document.createElement('span');el.textContent=value==null?'':String(value);return el.innerHTML}
document.getElementById('weeklyTable').innerHTML=periodTable(weekly);
document.getElementById('yearlyTable').innerHTML=periodTable(yearly);
document.getElementById('senderTable').innerHTML=rankTable(senders);
document.getElementById('recipientTable').innerHTML=rankTable(recipients);
document.getElementById('sourceTable').innerHTML=sourceTable(sources);
document.getElementById('folderTable').innerHTML=folderTable(folderStats);
document.getElementById('dedupeStats').innerHTML='<p><strong>Unique messages:</strong> '+dedupe.UniqueMessages+' · <strong>Duplicate records:</strong> '+dedupe.DuplicateRecords+' · <strong>Message-ID keys:</strong> '+dedupe.MessageIDKeys+' · <strong>Fallback keys:</strong> '+dedupe.FallbackKeys+'</p>';
document.getElementById('threadTable').innerHTML=threadTable(threads);
document.getElementById('attachmentTable').innerHTML=attachmentTable(attachments);
document.getElementById('warnings').innerHTML=warnings.length?'<table><thead><tr><th>Record</th><th>Warning</th></tr></thead><tbody>'+warnings.map(w=>' <tr><td>'+w.MessageIndex+'</td><td class="warning">'+escapeText(w.Text)+'</td></tr>').join('')+'</tbody></table>':'<p>No parse warnings.</p>';
drawChart(document.getElementById('weeklyChart'),weekly,'Period');
drawChart(document.getElementById('yearlyChart'),yearly,'Period');
</script>
</body></html>`))
}
