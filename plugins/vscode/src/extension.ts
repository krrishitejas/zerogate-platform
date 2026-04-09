import * as vscode from 'vscode';
import axios from 'axios';

let diagnosticCollection: vscode.DiagnosticCollection;

export function activate(context: vscode.ExtensionContext) {
	console.log('ZEROGATE extension is now active!');

	// Initialize diagnostics
	diagnosticCollection = vscode.languages.createDiagnosticCollection('zerogate');
	context.subscriptions.push(diagnosticCollection);

	// Command: Scan Current File
	const scanCmd = vscode.commands.registerCommand('zerogate.scanFile', async () => {
		const editor = vscode.window.activeTextEditor;
		if (!editor) return;

		const document = editor.document;
		const relativePath = vscode.workspace.asRelativePath(document.uri);
		
		const config = vscode.workspace.getConfiguration('zerogate');
		const apiEndpoint = config.get<string>('apiEndpoint');
		const projectId = config.get<string>('projectId');

		if (!projectId) {
			vscode.window.showWarningMessage('Please configure your ZEROGATE Project ID in settings.');
			return;
		}

		vscode.window.withProgress({
			location: vscode.ProgressLocation.Notification,
			title: `ZEROGATE scanning ${relativePath}...`,
			cancellable: false
		}, async (progress) => {
			try {
				// Mock MCP tool call via API wrap or direct agent request
				const response = await axios.get(`${apiEndpoint}/findings?project_id=${projectId}`);
				const findings = response.data.findings || [];

				// Filter findings for current file
				const fileFindings = findings.filter((f: any) => f.file_path === relativePath && f.status !== 'fixed');

				const diagnostics: vscode.Diagnostic[] = [];
				
				for (const f of fileFindings) {
					// Fallback to line 1 if start is 0 or unset
					const line = f.line_start && f.line_start > 0 ? f.line_start - 1 : 0;
					const range = new vscode.Range(line, 0, line, Number.MAX_VALUE);
					
					let severity = vscode.DiagnosticSeverity.Warning;
					if (f.severity === 'critical' || f.severity === 'high') {
						severity = vscode.DiagnosticSeverity.Error;
					}

					const diagnostic = new vscode.Diagnostic(
						range,
						`[ZG: ${f.rule_id}] ${f.title}\n${f.description}`,
						severity
					);
					diagnostic.code = "AI Auto-Fix Available";
					diagnostics.push(diagnostic);
				}

				diagnosticCollection.set(document.uri, diagnostics);

				if (fileFindings.length > 0) {
					vscode.window.showErrorMessage(`ZEROGATE found ${fileFindings.length} issues in this file.`);
				} else {
					vscode.window.showInformationMessage('ZEROGATE scan complete. No issues found!');
				}

			} catch (error: any) {
				vscode.window.showErrorMessage(`ZEROGATE scan failed: ${error.message}`);
			}
		});
	});

	context.subscriptions.push(scanCmd);

	// Provider setup for Code Actions (Quick Fixes)
	context.subscriptions.push(
		vscode.languages.registerCodeActionsProvider('*', new ZerogateFixProvider(), {
			providedCodeActionKinds: ZerogateFixProvider.providedCodeActionKinds
		})
	);
}

export class ZerogateFixProvider implements vscode.CodeActionProvider {
	public static readonly providedCodeActionKinds = [
		vscode.CodeActionKind.QuickFix
	];

	public provideCodeActions(document: vscode.TextDocument, range: vscode.Range | vscode.Selection, context: vscode.CodeActionContext, token: vscode.CancellationToken): vscode.CodeAction[] {
		// Filter diagnostics that belong to Zerogate
		const zgDiagnostics = context.diagnostics.filter(d => d.source === 'zerogate' || (d.message && d.message.includes('[ZG:')));

		return zgDiagnostics.map(diagnostic => this.createFix(document, diagnostic));
	}

	private createFix(document: vscode.TextDocument, diagnostic: vscode.Diagnostic): vscode.CodeAction {
		const action = new vscode.CodeAction('Generate AI Fix with ZEROGATE', vscode.CodeActionKind.QuickFix);
		action.diagnostics = [diagnostic];
		action.isPreferred = true;
		
		// In a full implementation, this would trigger the MCP tool generate_fix
		action.command = {
			command: 'zerogate.applyFix',
			title: 'Apply ZEROGATE Fix',
			tooltip: 'Ask ZEROGATE to generate an inline diff patch for this issue.'
		};
		return action;
	}
}

export function deactivate() {
	if (diagnosticCollection) {
		diagnosticCollection.clear();
	}
}
