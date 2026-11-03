// SPDX-License-Identifier: LicenseRef-Proprietary
// Copyright (C) 2026 Andy Dixon t/a Uncomplex

//! Licence window: status, free trial, activation with a licence key, online
//! check and deactivation. `licensing-client` does the work on a background
//! tokio thread; this module only shows its state and forwards button presses.

use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant, SystemTime};

use licensing_client::{ClientConfig, LicenseClient, LicenseError, LicenseState, VerifyingKey};

const PRODUCT_ID: &str = "ec8f91c0-4515-4abb-9f0d-8c01472082bf";
const SERVER_URL: &str = "https://licensing.uncomplex.co.uk";
/// Licence terms the user must accept before a trial or activation.
const TERMS: &str = include_str!("../../../LICENSE");
/// How long after Quit the app's own close prompt may take to appear.
const QUIT_GRACE: Duration = Duration::from_secs(2);
/// Production root key (root-2026-11); issuer keys rotate underneath it.
const ROOT_KEY: [u8; 32] = [
    0xe3, 0xf1, 0xdb, 0xfa, 0xc9, 0x09, 0x28, 0x64, 0xba, 0xc0, 0x47, 0xcb, 0xad, 0x9c, 0x91, 0x55, 0x99, 0x8e, 0x7a, 0xcb, 0xfe, 0x7c, 0x42, 0xc5, 0x63, 0xaf,
    0xb6, 0x9b, 0x30, 0xc5, 0xbb, 0xed,
];

#[derive(Default)]
struct Shared {
    client: Option<LicenseClient>,
    /// None until the cached lease has been loaded.
    state: Option<LicenseState>,
    /// Activation waiting for browser approval: (user code, verification URL).
    session: Option<(String, String)>,
    busy: bool,
    message: Option<String>,
}

enum Op {
    Trial,
    Activate(Option<String>),
    Check,
    Deactivate,
}

pub struct Licence {
    rt: tokio::runtime::Handle,
    shared: Arc<Mutex<Shared>>,
    task: Option<tokio::task::AbortHandle>,
    ctx: egui::Context,
    open_url: fn(&str),
    key: String,
    /// "I agree to the licence terms" ticked, and whether the terms are expanded.
    agreed: bool,
    show_terms: bool,
    /// Show the window even while licensed (Help ▸ Licence).
    pub open: bool,
    /// Set when the user chose Quit from the blocking window: when, and whether the app's
    /// own quit prompt has shown since. The gate stays hidden only while that prompt is up.
    quitting: Option<(Instant, bool)>,
}

impl Licence {
    /// Starts the licensing thread, loads the cached lease offline and renews it in the background.
    pub fn new(ctx: &egui::Context, open_url: fn(&str)) -> Self {
        let rt = tokio::runtime::Builder::new_current_thread().enable_all().build().expect("licensing runtime");
        let handle = rt.handle().clone();
        std::thread::Builder::new().name("licensing".into()).spawn(move || rt.block_on(std::future::pending::<()>())).expect("licensing thread");
        let shared = Arc::new(Mutex::new(Shared::default()));
        let (sh, c) = (shared.clone(), ctx.clone());
        handle.spawn(async move {
            let started = async {
                let root = VerifyingKey::from_bytes(&ROOT_KEY).map_err(|e| LicenseError::Config(e.to_string()))?;
                let client = LicenseClient::new(ClientConfig::new(PRODUCT_ID, SERVER_URL, vec![root])?).await?;
                client.initialise().await?;
                Ok::<_, LicenseError>(client)
            }
            .await;
            let client = match started {
                Ok(client) => client,
                Err(e) => {
                    let mut s = sh.lock().unwrap();
                    s.state = Some(LicenseState::ActivationRequired);
                    s.message = Some(format!("Licensing is unavailable: {e}"));
                    c.request_repaint();
                    return;
                }
            };
            let mut rx = client.subscribe();
            client.spawn_renewal();
            {
                let mut s = sh.lock().unwrap();
                s.state = Some(rx.borrow_and_update().clone());
                s.client = Some(client);
            }
            c.request_repaint();
            while rx.changed().await.is_ok() {
                sh.lock().unwrap().state = Some(rx.borrow_and_update().clone());
                c.request_repaint();
            }
        });
        Self { rt: handle, shared, task: None, ctx: ctx.clone(), open_url, key: String::new(), agreed: false, show_terms: false, open: false, quitting: None }
    }

    fn start(&mut self, op: Op) {
        let mut s = self.shared.lock().unwrap();
        let Some(client) = s.client.clone() else { return };
        s.busy = true;
        s.message = None;
        drop(s);
        let (sh, ctx, open_url) = (self.shared.clone(), self.ctx.clone(), self.open_url);
        let task = self.rt.spawn(async move {
            let result = perform(&client, op, &sh, &ctx, open_url).await;
            let mut s = sh.lock().unwrap();
            s.busy = false;
            s.session = None;
            s.message = Some(result.unwrap_or_else(|e| error_text(&e)));
            ctx.request_repaint();
        });
        self.task = Some(task.abort_handle());
    }

    fn cancel(&mut self) {
        if let Some(t) = self.task.take() {
            t.abort();
        }
        let mut s = self.shared.lock().unwrap();
        s.busy = false;
        s.session = None;
        s.message = None;
        if let Some(client) = s.client.clone() {
            // Re-evaluate the stored lease so the state leaves ActivationPending.
            self.rt.spawn(async move { client.initialise().await });
        }
    }

    /// Draws the window. It blocks the app while unlicensed and is otherwise opened from the menu.
    /// `quit_prompt`: the app is showing its own quit confirmation (unsaved work and so on).
    pub fn show(&mut self, ctx: &egui::Context, quit_prompt: bool) {
        if let Some((at, seen)) = &mut self.quitting {
            *seen |= quit_prompt;
            // Prompt dismissed (quit cancelled), or none appeared and the app is still running: re-arm.
            if !quit_prompt && (*seen || at.elapsed() > QUIT_GRACE) {
                self.quitting = None;
            } else {
                ctx.request_repaint_after(QUIT_GRACE);
            }
        }
        let (state, session, busy, message) = {
            let s = self.shared.lock().unwrap();
            (s.state.clone(), s.session.clone(), s.busy, s.message.clone())
        };
        let Some(state) = state else { return };
        let licensed = state.is_licensed();
        if !(self.open || (!licensed && self.quitting.is_none())) {
            return;
        }
        let mut op = None;
        let (mut close, mut cancel) = (false, false);
        let r = egui::Modal::new(egui::Id::new("licence")).show(ctx, |ui| {
            ui.set_width(420.0);
            ui.heading("Licence");
            ui.add_space(4.0);
            let colour = if licensed { ui.visuals().text_color() } else { ui.visuals().warn_fg_color };
            ui.label(egui::RichText::new(describe(&state)).strong().color(colour));
            let features = match &state {
                LicenseState::Active { features, .. } | LicenseState::Trial { features, .. } => features.as_slice(),
                _ => &[],
            };
            if !features.is_empty() {
                ui.label(format!("Features: {}", features.join(", ")));
            }
            if let Some(m) = &message {
                ui.add_space(4.0);
                ui.label(m);
            }
            ui.add_space(8.0);
            if let Some((code, url)) = &session {
                ui.label("Approve this computer in your browser. If it did not open, use the link below; it already includes this code.");
                ui.hyperlink(url);
                ui.label(egui::RichText::new(code).monospace().size(26.0).strong());
                ui.horizontal(|ui| {
                    if ui.button("Open browser").clicked() {
                        (self.open_url)(url);
                    }
                    if ui.button("Copy code").clicked() {
                        ui.ctx().copy_text(code.clone());
                    }
                    ui.spinner();
                    ui.label("Waiting for approval…");
                });
                cancel = ui.button("Cancel").clicked();
            } else if busy {
                ui.horizontal(|ui| {
                    ui.spinner();
                    ui.label("Contacting the licensing server…");
                });
                cancel = ui.button("Cancel").clicked();
            } else {
                if !licensed {
                    ui.label("Accept the licence terms, then enter your licence key or start a free trial.");
                }
                ui.horizontal(|ui| {
                    ui.checkbox(&mut self.agreed, "I agree to the licence terms");
                    if ui.link(if self.show_terms { "Hide terms" } else { "Read terms" }).clicked() {
                        self.show_terms = !self.show_terms;
                    }
                });
                if self.show_terms {
                    egui::ScrollArea::vertical().max_height(240.0).show(ui, |ui| ui.label(egui::RichText::new(TERMS).small()));
                }
                let agreed = self.agreed;
                ui.horizontal(|ui| {
                    ui.add(egui::TextEdit::singleline(&mut self.key).hint_text("Licence key (optional)").desired_width(260.0));
                    if ui
                        .add_enabled(agreed, egui::Button::new(if licensed { "Change licence" } else { "Activate" }))
                        .on_disabled_hover_text("Agree to the licence terms first")
                        .clicked()
                    {
                        let key = self.key.trim();
                        op = Some(Op::Activate((!key.is_empty()).then(|| key.to_string())));
                    }
                });
                ui.add_space(4.0);
                ui.horizontal(|ui| {
                    if !licensed
                        && state != LicenseState::TrialExpired
                        && ui.add_enabled(agreed, egui::Button::new("Start free trial")).on_disabled_hover_text("Agree to the licence terms first").clicked()
                    {
                        op = Some(Op::Trial);
                    }
                    if state != LicenseState::ActivationRequired
                        && ui.button("Check licence").on_hover_text("Verify this licence with the server now").clicked()
                    {
                        op = Some(Op::Check);
                    }
                    if licensed
                        && ui.button("Deactivate this computer").on_hover_text("Free this computer's slot so the licence can be used elsewhere").clicked()
                    {
                        op = Some(Op::Deactivate);
                    }
                });
            }
            ui.add_space(8.0);
            ui.separator();
            ui.horizontal(|ui| {
                if licensed {
                    close = ui.button("Close").clicked();
                } else if ui.button("Quit").clicked() {
                    self.quitting = Some((Instant::now(), false));
                    ui.ctx().send_viewport_cmd(egui::ViewportCommand::Close);
                }
            });
        });
        if cancel {
            self.cancel();
        }
        if let Some(op) = op {
            self.start(op);
        }
        if licensed && (close || r.should_close()) {
            self.open = false;
        }
    }
}

async fn perform(client: &LicenseClient, op: Op, sh: &Mutex<Shared>, ctx: &egui::Context, open_url: fn(&str)) -> Result<String, LicenseError> {
    let session = match op {
        Op::Check => return client.refresh().await.map(|s| format!("Checked with the server: {}", describe(&s))),
        Op::Deactivate => return client.deactivate().await.map(|()| "This computer has been deactivated.".into()),
        Op::Trial => client.request_trial().await?,
        Op::Activate(key) => client.begin_activation(key.as_deref()).await?,
    };
    // The complete link carries the code, so the customer only has to approve.
    open_url(&session.verification_uri_complete);
    sh.lock().unwrap().session = Some((session.user_code.clone(), session.verification_uri_complete.clone()));
    ctx.request_repaint();
    let s = client.finish_activation(session).await?;
    Ok(if s.is_licensed() { "Thank you, this computer is now activated.".into() } else { describe(&s) })
}

fn error_text(e: &LicenseError) -> String {
    match e {
        LicenseError::Problem { code, .. } if code == "INTENT_EXPIRED" => "The activation request expired. Please try again.".into(),
        LicenseError::Problem { code, .. } if code == "ACTIVATION_DENIED" => "The activation was declined in the browser.".into(),
        LicenseError::Problem { detail, .. } if !detail.is_empty() => detail.clone(),
        e if e.is_transient() => format!("Could not reach the licensing server ({e}). Check your connection and try again."),
        e => e.to_string(),
    }
}

fn days_until(t: SystemTime) -> u64 {
    t.duration_since(SystemTime::now()).map_or(0, |d| d.as_secs().div_ceil(86_400))
}

fn describe(s: &LicenseState) -> String {
    match s {
        LicenseState::Active { valid_until, .. } => match days_until(*valid_until) {
            d if d > 3650 => "Licensed.".into(),
            d => format!("Licensed, {d} day(s) remaining."),
        },
        LicenseState::Trial { expires_at, .. } => format!("Trial, {} day(s) remaining.", days_until(*expires_at)),
        LicenseState::ActivationRequired => "This computer is not activated.".into(),
        LicenseState::ActivationPending => "Waiting for activation to be approved.".into(),
        LicenseState::Scheduled { valid_from } => format!("Your licence starts in {} day(s).", days_until(*valid_from)),
        LicenseState::Expired => "Your licence has expired.".into(),
        LicenseState::TrialExpired => "Your trial has ended. Enter a licence key to keep using the app.".into(),
        LicenseState::Revoked => "This licence has been suspended or revoked. Please contact support.".into(),
        LicenseState::DeviceDeauthorised => "This computer was removed from the licence. Activate it again to continue.".into(),
        LicenseState::DeviceLimitReached => {
            "Your licence is in use on the maximum number of computers. Remove one in the customer portal, then activate again.".into()
        }
        LicenseState::OfflineLeaseExpired => "The licence has not been checked for seven days. Connect to the internet and choose Check licence.".into(),
        LicenseState::OnlineVerificationRequired => "The licence must be checked online. Choose Check licence.".into(),
        LicenseState::ClockIntegrityFailure => "The system clock moved backwards. Correct the clock, then choose Check licence.".into(),
        LicenseState::ServiceUnavailable => "The licensing server cannot be reached. Try again later.".into(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn terms_are_the_licence_agreement() {
        assert!(TERMS.starts_with("MASTER SOFTWARE LICENCE AND SERVICES AGREEMENT"));
    }

    #[test]
    fn root_key_is_valid() {
        assert!(VerifyingKey::from_bytes(&ROOT_KEY).is_ok());
    }

    #[test]
    fn days_round_up() {
        assert_eq!(days_until(SystemTime::now() + std::time::Duration::from_secs(86_400 + 60)), 2);
        assert_eq!(days_until(SystemTime::now() - std::time::Duration::from_secs(60)), 0);
    }
}
