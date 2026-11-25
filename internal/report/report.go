// Package report renders mailbox summaries as standalone HTML documents.
package report

import (
	"encoding/json"
	"html/template"
	"os"
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
	InferenceJSON   template.JS
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
		InferenceJSON:   mustJSON(summary.Inference),
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	return htmlTemplate().Execute(file, data)
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
:root{color-scheme:light dark;--bg:#f6f7fb;--panel:#fff;--text:#1d2433;--muted:#667085;--blue:#2563eb;--green:#16a34a}*{box-sizing:border-box}body{margin:0;font:15px/1.5 system-ui,sans-serif;background:var(--bg);color:var(--text)}main{max-width:1200px;margin:auto;padding:24px}.cards{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:12px}.card,.panel{background:var(--panel);border:1px solid #d0d5dd;border-radius:10px;padding:16px;margin:12px 0}.card strong{display:block;font-size:28px}table{border-collapse:collapse;width:100%}th,td{border-bottom:1px solid #e4e7ec;text-align:left;padding:8px}canvas{width:100%;height:300px;border:1px solid #e4e7ec;border-radius:8px}.split{display:grid;grid-template-columns:1fr 1fr;gap:16px}.tag{display:inline-block;border-radius:12px;padding:2px 8px;background:#e8eefc;color:#1d4ed8}.warning{color:#b42318}@media(max-width:800px){.split{grid-template-columns:1fr}}@media print{body{background:#fff}.panel,.card{break-inside:avoid}}
</style>
</head>
<body><main>
<h1>{{.Title}}</h1>
<p>Cutoff: {{.Cutoff}} · Generated: {{.GeneratedAt}}</p>
<nav><a href="#weekly">Weekly</a> · <a href="#yearly">Yearly</a> · <a href="#senders">Senders</a> · <a href="#recipients">Recipients</a></nav>
<section class="cards">
<div class="card">Messages<strong>{{.MessageCount}}</strong></div>
<div class="card">Mailbox identities<strong style="font-size:16px">{{.OwnerIdentities}}</strong></div>
<div class="card">Parse warnings<strong>{{.WarningCount}}</strong></div>
</section>
<section id="weekly" class="panel"><h2>Weekly volume</h2><canvas id="weeklyChart"></canvas><div id="weeklyTable"></div></section>
<section id="yearly" class="panel"><h2>Yearly volume</h2><canvas id="yearlyChart"></canvas><div id="yearlyTable"></div></section>
<div class="split">
<section id="senders" class="panel"><h2>Top senders</h2><div id="senderTable"></div></section>
<section id="recipients" class="panel"><h2>Top recipients</h2><div id="recipientTable"></div></section>
</div>
<section class="panel"><h2>Identity inference</h2><div id="inference"></div></section>
<section class="panel"><h2>Parse warnings</h2><div id="warnings"></div></section>
</main>
<script>
const weekly={{.WeeklyJSON}};
const yearly={{.YearlyJSON}};
const senders={{.SendersJSON}};
const recipients={{.RecipientsJSON}};
const warnings={{.WarningsJSON}};
const inference={{.InferenceJSON}};

function drawChart(canvas, rows, labelKey) {
 const ctx=canvas.getContext('2d'); const dpr=window.devicePixelRatio||1;
 const rect=canvas.getBoundingClientRect(); canvas.width=rect.width*dpr; canvas.height=300*dpr; ctx.scale(dpr,dpr);
 ctx.clearRect(0,0,rect.width,300); if(!rows.length)return;
 const max=Math.max(1,...rows.map(r=>Math.max(r.Sent,r.Received)));
 const left=42,right=10,top=12,bottom=28,w=rect.width-left-right,h=270;
 ctx.strokeStyle='#98a2b3';ctx.beginPath();ctx.moveTo(left,top);ctx.lineTo(left,top+h);ctx.lineTo(left+w,top+h);ctx.stroke();
 function line(key,color){
  ctx.strokeStyle=color;ctx.beginPath();
  rows.forEach((r,i)=>{const x=left+(rows.length===1?0:i*w/(rows.length-1));const y=top+h-r[key]/max*h;if(i===0)ctx.moveTo(x,y);else ctx.lineTo(x,y)});
  ctx.stroke();
 }
 line('Sent','#2563eb');line('Received','#16a34a');
 ctx.fillStyle='#667085';ctx.font='11px system-ui';
 if(rows.length<=12){rows.forEach((r,i)=>{const x=left+(rows.length===1?0:i*w/(rows.length-1));ctx.fillText(r[labelKey],x-12,295)});}
 ctx.fillText('Sent: blue',left,8);ctx.fillStyle='#16a34a';ctx.fillText('Received: green',left+70,8);
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
function escapeText(value){const el=document.createElement('span');el.textContent=value==null?'':String(value);return el.innerHTML}
document.getElementById('weeklyTable').innerHTML=periodTable(weekly);
document.getElementById('yearlyTable').innerHTML=periodTable(yearly);
document.getElementById('senderTable').innerHTML=rankTable(senders);
document.getElementById('recipientTable').innerHTML=rankTable(recipients);
document.getElementById('inference').innerHTML='<p><strong>Address:</strong> '+escapeText(inference.Address||'(not inferred)')+'</p><p><strong>Confidence:</strong> '+(inference.Confident?'Confident':'Ambiguous')+'</p><p><strong>Folder evidence:</strong> '+inference.FolderEvidence+' · <strong>Sent frequency:</strong> '+inference.Frequency+'</p><p><strong>Candidates:</strong> '+((inference.Candidates||[]).map(escapeText).join(', ')||'none')+'</p>';
document.getElementById('warnings').innerHTML=warnings.length?'<table><thead><tr><th>Record</th><th>Warning</th></tr></thead><tbody>'+warnings.map(w=>' <tr><td>'+w.MessageIndex+'</td><td class="warning">'+escapeText(w.Text)+'</td></tr>').join('')+'</tbody></table>':'<p>No parse warnings.</p>';
drawChart(document.getElementById('weeklyChart'),weekly,'Period');
drawChart(document.getElementById('yearlyChart'),yearly,'Period');
window.addEventListener('resize',()=>{drawChart(document.getElementById('weeklyChart'),weekly,'Period');drawChart(document.getElementById('yearlyChart'),yearly,'Period')});
</script>
</body></html>`))
}
