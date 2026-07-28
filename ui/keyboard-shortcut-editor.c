#include <ctype.h>
#include <string.h>
#include <gtk/gtk.h>
#include "_cgo_export.h"

#define TOTAL_ROWS 4
#define IBdisableOnGame 1<<21

int recording_row = -1;
guint32 *key_pairs_tmp;
char *app_version = "";
GtkWidget *shortcut_buttons[TOTAL_ROWS];
GtkWidget *stack;
GtkWidget *spell_switch = NULL, *rules_switch = NULL, *dicts_switch = NULL;

char *shortcut_names[] = {
	"Chuyển chế độ gõ", "Khôi phục phím",
	"Tạm tắt bộ gõ", "Emoji",
};

char *input_mode_alert =
	"Các chế độ gõ:\n"
	"  Kernel - gõ qua evdev (không gạch chân)\n"
	"  Preedit - gõ qua IBus preedit (có gạch chân)\n"
	"  Surround - gõ qua SurroundingText\n\n"
	"Mỗi ứng dụng có thể đặt chế độ gõ riêng.\n"
	"Nhấn phím tắt Chuyển chế độ gõ để đổi nhanh.";

char *fix_fb_alert =
	"Bật tùy chọn này nếu bạn gặp tình trạng lặp chữ "
	"khi chat trong Facebook, Messenger.\n"
	"Lưu ý: Tính năng này có thể khiến thanh địa chỉ "
	"trên trình duyệt Google Chrome hoạt động không chính xác.";

char *mode_names[] = {"Kernel", "Preedit", "Surround", "Ignore"};

static void update_shortcut_label(int row);
static void add_app_list_page(GtkWidget *parent, const char *mappings, int filter_mode, const char *placeholder);
static GtkWidget* make_app_row(const char *app, int mode);

static void save_shortcuts(void) {
	saveShortcuts(key_pairs_tmp, TOTAL_ROWS * 2);
}

static void btn_reset_cb(GtkWidget *widget, gpointer data) {
	for (int i = 0; i < TOTAL_ROWS * 2; i++)
		key_pairs_tmp[i] = 0;
	for (int i = TOTAL_ROWS * 2; i < 10; i++)
		key_pairs_tmp[i] = 0;
	for (int i = 0; i < TOTAL_ROWS; i++)
		update_shortcut_label(i);
	save_shortcuts();
}

static void close_window(GtkWidget *widget, gpointer data) {
	gtk_widget_destroy(GTK_WIDGET(data ? data : widget));
}

static void btn_macro_save_cb(GtkWidget *widget, gpointer data) {
	GtkTextBuffer *buffer = g_object_get_data(G_OBJECT(widget), "buffer");
	int saveMacro = GPOINTER_TO_INT(g_object_get_data(G_OBJECT(widget), "saveMacroText"));
	GtkTextIter start, end;
	gtk_text_buffer_get_bounds(buffer, &start, &end);
	gchar *text = gtk_text_buffer_get_text(buffer, &start, &end, FALSE);
	if (saveMacro) saveMacroText(text);
	else saveConfigText(text);
	g_free(text);
	close_window(widget, data);
}

static char *format_accel(guint keyval, guint mask) {
	gchar *key_name = gtk_accelerator_get_label(keyval, 0);
	char *s = key_name;
	while (*s) { *s = toupper((unsigned char)*s); s++; }
	return key_name;
}

static void update_shortcut_label(int row) {
	guint keyval = key_pairs_tmp[row * 2 + 1];
	guint mask = key_pairs_tmp[row * 2];
	if (keyval == 0 && mask == 0) {
		gtk_button_set_label(GTK_BUTTON(shortcut_buttons[row]), "Chưa đặt");
		return;
	}
	gchar *accel = format_accel(keyval, mask);
	char buf[128] = "";
	if (mask & GDK_CONTROL_MASK) strcat(buf, "Ctrl+");
	if (mask & GDK_MOD1_MASK) strcat(buf, "Alt+");
	if (mask & GDK_SHIFT_MASK) strcat(buf, "Shift+");
	if (mask & GDK_SUPER_MASK) strcat(buf, "Super+");
	strcat(buf, accel);
	g_free(accel);
	gtk_button_set_label(GTK_BUTTON(shortcut_buttons[row]), buf);
}

static gboolean shortcut_key_press(GtkWidget *w, GdkEventKey *event, gpointer data) {
	int row = GPOINTER_TO_INT(data);
	if (row != recording_row) return FALSE;
	if (event->keyval == GDK_KEY_Escape) {
		recording_row = -1;
		gtk_button_set_label(GTK_BUTTON(shortcut_buttons[row]), "Chưa đặt");
		save_shortcuts();
		return TRUE;
	}
	if (event->keyval == GDK_KEY_BackSpace || event->keyval == GDK_KEY_Delete) {
		key_pairs_tmp[row * 2] = 0;
		key_pairs_tmp[row * 2 + 1] = 0;
		recording_row = -1;
		update_shortcut_label(row);
		save_shortcuts();
		return TRUE;
	}
	if (event->is_modifier) return TRUE;
	guint mask = event->state & gtk_accelerator_get_default_mod_mask();
	guint keyval = gdk_keyval_to_lower(event->keyval);
	key_pairs_tmp[row * 2] = mask;
	key_pairs_tmp[row * 2 + 1] = keyval;
	recording_row = -1;
	update_shortcut_label(row);
	save_shortcuts();
	return TRUE;
}

static void shortcut_clicked(GtkWidget *btn, gpointer data) {
	int row = GPOINTER_TO_INT(data);
	if (recording_row == row) { recording_row = -1; update_shortcut_label(row); return; }
	recording_row = row;
	gtk_button_set_label(GTK_BUTTON(btn), "Nhấn tổ hợp phím...");
	gtk_widget_grab_focus(btn);
}

static void add_shortcut_page(GtkWidget *parent, GtkWidget *win) {
	GtkWidget *vbox = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
	GtkWidget *sw = gtk_scrolled_window_new(NULL, NULL);
	gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(sw),
		GTK_POLICY_NEVER, GTK_POLICY_AUTOMATIC);

	GtkWidget *list = gtk_list_box_new();
	gtk_list_box_set_selection_mode(GTK_LIST_BOX(list), GTK_SELECTION_NONE);
	gtk_widget_set_margin_start(list, 12);
	gtk_widget_set_margin_end(list, 12);
	gtk_widget_set_margin_top(list, 12);

	for (int i = 0; i < TOTAL_ROWS; i++) {
		GtkWidget *row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0);
		gtk_widget_set_margin_start(row, 12);
		gtk_widget_set_margin_end(row, 12);
		gtk_widget_set_margin_top(row, 8);
		gtk_widget_set_margin_bottom(row, 8);

		GtkWidget *label = gtk_label_new(shortcut_names[i]);
		gtk_label_set_xalign(GTK_LABEL(label), 0);
		gtk_widget_set_hexpand(label, TRUE);

		GtkWidget *btn = gtk_button_new_with_label("Chưa đặt");
		gtk_widget_set_size_request(btn, 200, -1);
		gtk_style_context_add_class(gtk_widget_get_style_context(btn), "flat");
		shortcut_buttons[i] = btn;

		g_signal_connect(btn, "clicked", G_CALLBACK(shortcut_clicked), GINT_TO_POINTER(i));
		g_signal_connect(btn, "key_press_event", G_CALLBACK(shortcut_key_press), GINT_TO_POINTER(i));

		gtk_box_pack_start(GTK_BOX(row), label, TRUE, TRUE, 0);
		gtk_box_pack_end(GTK_BOX(row), btn, FALSE, FALSE, 6);

		GtkWidget *lrow = gtk_list_box_row_new();
		gtk_container_add(GTK_CONTAINER(lrow), row);
		gtk_list_box_insert(GTK_LIST_BOX(list), lrow, -1);

		update_shortcut_label(i);
	}

	gtk_container_add(GTK_CONTAINER(sw), list);
	gtk_box_pack_start(GTK_BOX(vbox), sw, TRUE, TRUE, 0);

	GtkWidget *hbox = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0);
	gtk_widget_set_halign(hbox, GTK_ALIGN_END);
	gtk_widget_set_margin_top(hbox, 8);
	gtk_widget_set_margin_bottom(hbox, 12);
	gtk_widget_set_margin_start(hbox, 12);
	gtk_widget_set_margin_end(hbox, 12);

	GtkWidget *btn = gtk_button_new_with_label("Reset");
	gtk_box_pack_end(GTK_BOX(hbox), btn, FALSE, FALSE, 6);
	g_signal_connect(btn, "clicked", G_CALLBACK(btn_reset_cb), NULL);

	gtk_box_pack_end(GTK_BOX(vbox), hbox, FALSE, FALSE, 0);
	gtk_box_pack_start(GTK_BOX(parent), vbox, TRUE, TRUE, 0);
}

// ── Macro table (Typing tab) ──
static GtkWidget *macro_list = NULL;
static GtkWidget *macro_entry_key = NULL;
static GtkWidget *macro_entry_val = NULL;
static GtkWidget *macro_win = NULL;

static void macro_save_all(void) {
	GString *buf = g_string_new("# DO NOT DELETE THIS LINE*** version=1 ***\n#\n");
	GtkWidget *row;
	GList *children = gtk_container_get_children(GTK_CONTAINER(macro_list));
	for (GList *c = children; c; c = c->next) {
		row = GTK_WIDGET(c->data);
		if (!GTK_IS_LIST_BOX_ROW(row)) continue;
		GtkWidget *box = gtk_bin_get_child(GTK_BIN(row));
		if (!box) continue;
		GtkWidget *key_entry = g_object_get_data(G_OBJECT(row), "key_entry");
		GtkWidget *val_entry = g_object_get_data(G_OBJECT(row), "val_entry");
		if (!key_entry || !val_entry) continue;
		const char *key = gtk_entry_get_text(GTK_ENTRY(key_entry));
		const char *val = gtk_entry_get_text(GTK_ENTRY(val_entry));
		if (strlen(key) == 0) continue;
		g_string_append_printf(buf, "%s:%s\n", key, val);
	}
	g_list_free(children);
	saveMacroText(buf->str);
	g_string_free(buf, TRUE);
}

static gboolean macro_focus_out(GtkWidget *w, GdkEvent *e, gpointer data) {
	macro_save_all();
	return FALSE;
}

static void macro_save_clicked(GtkWidget *btn, gpointer data) {
	macro_save_all();
}

static void macro_delete_row(GtkWidget *btn, gpointer data) {
	GtkWidget *row = GTK_WIDGET(data);
	GtkWidget *win = gtk_widget_get_toplevel(btn);
	GtkWidget *dialog = gtk_message_dialog_new(GTK_WINDOW(win),
		GTK_DIALOG_MODAL | GTK_DIALOG_DESTROY_WITH_PARENT,
		GTK_MESSAGE_QUESTION, GTK_BUTTONS_YES_NO,
		"Xoá mục gõ tắt này?");
	gint res = gtk_dialog_run(GTK_DIALOG(dialog));
	gtk_widget_destroy(dialog);
	if (res == GTK_RESPONSE_YES) {
		gtk_widget_destroy(row);
		macro_save_all();
	}
}

static GtkWidget* macro_create_row(const char *key, const char *val) {
	GtkWidget *row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0);
	gtk_widget_set_margin_start(row, 0); gtk_widget_set_margin_end(row, 0);
	gtk_widget_set_margin_top(row, 3); gtk_widget_set_margin_bottom(row, 3);

	GtkWidget *ke = gtk_entry_new();
	gtk_entry_set_text(GTK_ENTRY(ke), key ? key : "");
	gtk_widget_set_hexpand(ke, TRUE);
	gtk_entry_set_placeholder_text(GTK_ENTRY(ke), "Từ viết tắt");
	g_signal_connect(ke, "focus-out-event", G_CALLBACK(macro_focus_out), NULL);

	GtkWidget *ve = gtk_entry_new();
	gtk_entry_set_text(GTK_ENTRY(ve), val ? val : "");
	gtk_widget_set_hexpand(ve, TRUE);
	gtk_entry_set_placeholder_text(GTK_ENTRY(ve), "Nội dung thay thế");
	g_signal_connect(ve, "focus-out-event", G_CALLBACK(macro_focus_out), NULL);

	GtkWidget *ok = gtk_button_new_with_label("✓");
	gtk_widget_set_size_request(ok, 24, 24);
	gtk_style_context_add_class(gtk_widget_get_style_context(ok), "circular");
	g_signal_connect(ok, "clicked", G_CALLBACK(macro_save_clicked), NULL);

	GtkWidget *del = gtk_button_new_with_label("✕");
	gtk_widget_set_size_request(del, 24, 24);
	gtk_style_context_add_class(gtk_widget_get_style_context(del), "destructive-action");
	gtk_style_context_add_class(gtk_widget_get_style_context(del), "circular");

	gtk_box_pack_start(GTK_BOX(row), ke, TRUE, TRUE, 6);
	gtk_box_pack_start(GTK_BOX(row), ve, TRUE, TRUE, 6);
	gtk_box_pack_end(GTK_BOX(row), del, FALSE, FALSE, 2);
	gtk_box_pack_end(GTK_BOX(row), ok, FALSE, FALSE, 0);

	GtkWidget *lrow = gtk_list_box_row_new();
	gtk_container_add(GTK_CONTAINER(lrow), row);
	g_object_set_data(G_OBJECT(lrow), "key_entry", ke);
	g_object_set_data(G_OBJECT(lrow), "val_entry", ve);
	g_signal_connect(del, "clicked", G_CALLBACK(macro_delete_row), lrow);
	return lrow;
}

static void macro_add_row(GtkWidget *btn, gpointer data) {
	const char *key = gtk_entry_get_text(GTK_ENTRY(macro_entry_key));
	const char *val = gtk_entry_get_text(GTK_ENTRY(macro_entry_val));
	if (strlen(key) == 0) return;

	GtkWidget *lrow = macro_create_row(key, val);
	gtk_list_box_insert(GTK_LIST_BOX(macro_list), lrow, -1);
	gtk_entry_set_text(GTK_ENTRY(macro_entry_key), "");
	gtk_entry_set_text(GTK_ENTRY(macro_entry_val), "");
	gtk_widget_show_all(lrow);
	macro_save_all();
}

static void add_typing_page(GtkWidget *parent, GtkWidget *win, const char *macro_text) {
	macro_win = win;
	GtkWidget *vbox = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
	gtk_widget_set_margin_start(vbox, 12); gtk_widget_set_margin_end(vbox, 12);
	gtk_widget_set_margin_top(vbox, 12);

	// Header row
	GtkWidget *header = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0);
	gtk_widget_set_margin_bottom(header, 6);
	GtkWidget *hl = gtk_label_new("Từ viết tắt");
	gtk_widget_set_hexpand(hl, TRUE);
	gtk_label_set_xalign(GTK_LABEL(hl), 0);
	GtkWidget *hr = gtk_label_new("Nội dung thay thế");
	gtk_widget_set_hexpand(hr, TRUE);
	gtk_label_set_xalign(GTK_LABEL(hr), 0);
	gtk_box_pack_start(GTK_BOX(header), hl, TRUE, TRUE, 6);
	gtk_box_pack_start(GTK_BOX(header), hr, TRUE, TRUE, 6);
	gtk_box_pack_start(GTK_BOX(header), gtk_label_new(""), FALSE, FALSE, 0);
	gtk_box_pack_start(GTK_BOX(vbox), header, FALSE, FALSE, 0);

	// Scrollable list
	GtkWidget *sw = gtk_scrolled_window_new(NULL, NULL);
	gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(sw), GTK_POLICY_NEVER, GTK_POLICY_AUTOMATIC);
	gtk_widget_set_vexpand(sw, TRUE);

	macro_list = gtk_list_box_new();
	gtk_list_box_set_selection_mode(GTK_LIST_BOX(macro_list), GTK_SELECTION_NONE);

	// Parse existing macros
	if (macro_text && strlen(macro_text) > 0) {
		char *copy = g_strdup(macro_text);
		char *save;
		char *line = strtok_r(copy, "\n", &save);
		while (line) {
			if (line[0] == '#' || strlen(line) < 3) { line = strtok_r(NULL, "\n", &save); continue; }
			char *colon = strchr(line, ':');
			if (!colon) { line = strtok_r(NULL, "\n", &save); continue; }
			*colon = '\0';
			char *key = line;
			char *val = colon + 1;
			if (strlen(key) == 0) { line = strtok_r(NULL, "\n", &save); continue; }

			GtkWidget *lrow2 = macro_create_row(key, val);
			gtk_list_box_insert(GTK_LIST_BOX(macro_list), lrow2, -1);

			line = strtok_r(NULL, "\n", &save);
		}
		g_free(copy);
	}

	gtk_container_add(GTK_CONTAINER(sw), macro_list);
	gtk_box_pack_start(GTK_BOX(vbox), sw, TRUE, TRUE, 0);

	// Add bar
	GtkWidget *addbox = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0);
	gtk_widget_set_margin_top(addbox, 8); gtk_widget_set_margin_bottom(addbox, 12);

	macro_entry_key = gtk_entry_new();
	gtk_entry_set_placeholder_text(GTK_ENTRY(macro_entry_key), "vn");
	gtk_widget_set_hexpand(macro_entry_key, TRUE);

	macro_entry_val = gtk_entry_new();
	gtk_entry_set_placeholder_text(GTK_ENTRY(macro_entry_val), "Việt Nam");
	gtk_widget_set_hexpand(macro_entry_val, TRUE);

	GtkWidget *add_btn = gtk_button_new_with_label("Thêm");
	g_signal_connect(add_btn, "clicked", G_CALLBACK(macro_add_row), NULL);

	gtk_box_pack_start(GTK_BOX(addbox), macro_entry_key, TRUE, TRUE, 6);
	gtk_box_pack_start(GTK_BOX(addbox), macro_entry_val, TRUE, TRUE, 6);
	gtk_box_pack_end(GTK_BOX(addbox), add_btn, FALSE, FALSE, 0);
	gtk_box_pack_start(GTK_BOX(vbox), addbox, FALSE, FALSE, 0);
	gtk_box_pack_start(GTK_BOX(parent), vbox, TRUE, TRUE, 0);
}

static void app_mode_changed(GtkComboBox *combo, gpointer data) {
	GtkTreeIter iter;
	if (!gtk_combo_box_get_active_iter(combo, &iter)) return;
	GtkTreeModel *model = gtk_combo_box_get_model(combo);
	gint mode;
	gtk_tree_model_get(model, &iter, 1, &mode, -1);
	const char *app = (const char *)g_object_get_data(G_OBJECT(combo), "app_name");
	saveAppMode((char*)app, mode);

	// If app switched to/from Ignore, remove from this list so the other tab shows it
	GtkWidget *row = gtk_widget_get_parent(GTK_WIDGET(combo));
	if (!row) return;
	GtkWidget *lrow = gtk_widget_get_parent(row);
	if (!lrow) return;
	GtkWidget *list = gtk_widget_get_parent(lrow);
	int is_ignore = (mode == 3);
	int tab_is_ignore = GPOINTER_TO_INT(g_object_get_data(G_OBJECT(list), "is_ignore"));
	if (is_ignore != tab_is_ignore)
		gtk_widget_destroy(lrow);
}

static void app_delete_clicked(GtkWidget *btn, gpointer data) {
	const char *app = (const char *)g_object_get_data(G_OBJECT(btn), "app_name");
	GtkWidget *win = gtk_widget_get_toplevel(btn);
	char msg[256];
	snprintf(msg, sizeof(msg), "Xoá thiết lập cho ứng dụng \"%s\"?", app);
	GtkWidget *dialog = gtk_message_dialog_new(GTK_WINDOW(win),
		GTK_DIALOG_MODAL | GTK_DIALOG_DESTROY_WITH_PARENT,
		GTK_MESSAGE_QUESTION, GTK_BUTTONS_YES_NO, "%s", msg);
	gint res = gtk_dialog_run(GTK_DIALOG(dialog));
	gtk_widget_destroy(dialog);
	if (res == GTK_RESPONSE_YES) {
		removeAppMode((char*)app);
		GtkWidget *row = gtk_widget_get_parent(gtk_widget_get_parent(btn));
		if (row) gtk_widget_destroy(row);
	}
}

static void add_app_clicked(GtkWidget *btn, gpointer data) {
	GtkWidget *list = GTK_WIDGET(data);
	GtkWidget *entry = g_object_get_data(G_OBJECT(btn), "entry");
	int filter_mode = GPOINTER_TO_INT(g_object_get_data(G_OBJECT(btn), "filter_mode"));
	int default_mode = (filter_mode == 3) ? 3 : 0;
	const char *app = gtk_entry_get_text(GTK_ENTRY(entry));
	if (strlen(app) == 0) return;
	saveAppMode((char*)app, default_mode);

	GtkWidget *row = make_app_row(app, default_mode);
	GtkWidget *lrow = gtk_list_box_row_new();
	gtk_container_add(GTK_CONTAINER(lrow), row);
	gtk_list_box_insert(GTK_LIST_BOX(list), lrow, 0);
	gtk_entry_set_text(GTK_ENTRY(entry), "");
	gtk_widget_show_all(row);
}

static GtkWidget* make_app_row(const char *app, int mode) {
	GtkWidget *row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0);
	gtk_widget_set_margin_start(row, 12); gtk_widget_set_margin_end(row, 12);
	gtk_widget_set_margin_top(row, 4); gtk_widget_set_margin_bottom(row, 4);

	GtkWidget *lbl = gtk_label_new(app);
	gtk_label_set_xalign(GTK_LABEL(lbl), 0);
	gtk_widget_set_hexpand(lbl, TRUE);

	GtkWidget *combo = gtk_combo_box_text_new();
	for (int i = 0; i < 4; i++)
		gtk_combo_box_text_append(GTK_COMBO_BOX_TEXT(combo), NULL, mode_names[i]);
	gtk_combo_box_set_active(GTK_COMBO_BOX(combo), mode);
	g_object_set_data(G_OBJECT(combo), "app_name", g_strdup(app));
	g_signal_connect(combo, "changed", G_CALLBACK(app_mode_changed), NULL);

	GtkWidget *del = gtk_button_new_with_label("✕");
	gtk_widget_set_size_request(del, 24, 24);
	gtk_style_context_add_class(gtk_widget_get_style_context(del), "destructive-action");
	gtk_style_context_add_class(gtk_widget_get_style_context(del), "circular");
	g_object_set_data(G_OBJECT(del), "app_name", g_strdup(app));
	g_signal_connect(del, "clicked", G_CALLBACK(app_delete_clicked), NULL);

	gtk_box_pack_start(GTK_BOX(row), lbl, TRUE, TRUE, 0);
	gtk_box_pack_end(GTK_BOX(row), del, FALSE, FALSE, 6);
	gtk_box_pack_end(GTK_BOX(row), combo, FALSE, FALSE, 6);
	return row;
}

typedef struct { GtkWidget *list; int filter_mode; } PopulateCtx;

static void populate_app_list(GtkListBox *list, const char *data, int filter_mode) {
	if (!data || strlen(data) == 0) return;
	char *copy = g_strdup(data);
	char *save1, *save2;
	char *entry = strtok_r(copy, ";", &save1);
	while (entry) {
		char *app = strtok_r(entry, "|", &save2);
		char *modestr = strtok_r(NULL, "|", &save2);
		if (!app || !modestr) { entry = strtok_r(NULL, ";", &save1); continue; }
		int mode = atoi(modestr);
		if (filter_mode >= 0 && mode != filter_mode) { entry = strtok_r(NULL, ";", &save1); continue; }
		if (filter_mode == -1 && mode == 3) { entry = strtok_r(NULL, ";", &save1); continue; }
		GtkWidget *row = make_app_row(app, mode);
		GtkWidget *lrow = gtk_list_box_row_new();
		gtk_container_add(GTK_CONTAINER(lrow), row);
		gtk_list_box_insert(GTK_LIST_BOX(list), lrow, -1);
		entry = strtok_r(NULL, ";", &save1);
	}
	g_free(copy);
}

static void add_app_list_page(GtkWidget *parent, const char *mappings, int filter_mode, const char *placeholder) {
	GtkWidget *vbox = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
	GtkWidget *sw = gtk_scrolled_window_new(NULL, NULL);
	gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(sw), GTK_POLICY_NEVER, GTK_POLICY_AUTOMATIC);
	gtk_widget_set_vexpand(sw, TRUE);

	GtkWidget *list = gtk_list_box_new();
	gtk_list_box_set_selection_mode(GTK_LIST_BOX(list), GTK_SELECTION_NONE);
	gtk_widget_set_margin_start(list, 12);
	gtk_widget_set_margin_end(list, 12);
	gtk_widget_set_margin_top(list, 12);
	g_object_set_data(G_OBJECT(list), "is_ignore", GINT_TO_POINTER(filter_mode == 3));

	populate_app_list(GTK_LIST_BOX(list), mappings, filter_mode);
	gtk_container_add(GTK_CONTAINER(sw), list);
	gtk_box_pack_start(GTK_BOX(vbox), sw, TRUE, TRUE, 0);

	GtkWidget *addbox = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0);
	gtk_widget_set_margin_start(addbox, 12); gtk_widget_set_margin_end(addbox, 12);
	gtk_widget_set_margin_top(addbox, 8); gtk_widget_set_margin_bottom(addbox, 12);

	GtkWidget *entry = gtk_entry_new();
	gtk_entry_set_placeholder_text(GTK_ENTRY(entry), placeholder);
	gtk_widget_set_hexpand(entry, TRUE);
	GtkWidget *btn = gtk_button_new_with_label("Thêm");
	g_object_set_data(G_OBJECT(btn), "entry", entry);
	g_object_set_data(G_OBJECT(btn), "filter_mode", GINT_TO_POINTER(filter_mode));
	g_signal_connect(btn, "clicked", G_CALLBACK(add_app_clicked), list);
	gtk_box_pack_start(GTK_BOX(addbox), entry, TRUE, TRUE, 6);
	gtk_box_pack_end(GTK_BOX(addbox), btn, FALSE, FALSE, 6);
	gtk_box_pack_start(GTK_BOX(vbox), addbox, FALSE, FALSE, 0);
	gtk_box_pack_start(GTK_BOX(parent), vbox, TRUE, TRUE, 0);
}

static void combo_changed_cb(GtkComboBox *combo, gpointer data) {
	GtkTreeIter iter;
	if (gtk_combo_box_get_active_iter(combo, &iter)) {
		GtkTreeModel *model = gtk_combo_box_get_model(combo);
		gint effect;
		gtk_tree_model_get(model, &iter, 1, &effect, -1);
		saveInputMode(effect);
	}
}

static GtkWidget* make_dropdown(int mode) {
	GtkListStore *store = gtk_list_store_new(2, G_TYPE_STRING, G_TYPE_INT);
	for (int i = 0; i < 3; i++) {
		GtkTreeIter iter;
		gtk_list_store_append(store, &iter);
		gtk_list_store_set(store, &iter, 0, mode_names[i], 1, i, -1);
	}
	GtkWidget *combo = gtk_combo_box_new_with_model(GTK_TREE_MODEL(store));
	g_object_unref(store);
	gtk_combo_box_set_active(GTK_COMBO_BOX(combo), mode);
	g_signal_connect(combo, "changed", G_CALLBACK(combo_changed_cb), NULL);
	GtkCellRenderer *r = gtk_cell_renderer_text_new();
	gtk_cell_layout_pack_start(GTK_CELL_LAYOUT(combo), r, TRUE);
	gtk_cell_layout_set_attributes(GTK_CELL_LAYOUT(combo), r, "text", 0, NULL);
	return combo;
}

static void im_changed_cb(GtkComboBox *c, gpointer d) {
	gchar *name = gtk_combo_box_text_get_active_text(GTK_COMBO_BOX_TEXT(c));
	if (name) { saveInputMethodName(name); g_free(name); }
}
static void cs_changed_cb(GtkComboBox *c, gpointer d) {
	gchar *name = gtk_combo_box_text_get_active_text(GTK_COMBO_BOX_TEXT(c));
	if (name) { saveOutputCharset(name); g_free(name); }
}
static void core_flag_cb(GObject *o, GParamSpec *p, gpointer d) {
	saveCoreFlag(GPOINTER_TO_UINT(g_object_get_data(o, "flag")),
		gtk_switch_get_active(GTK_SWITCH(o)));
}
static void ib_flag_cb(GObject *o, GParamSpec *p, gpointer d) {
	guint flag = GPOINTER_TO_UINT(g_object_get_data(o, "flag"));
	gboolean active = gtk_switch_get_active(GTK_SWITCH(o));
	saveIBFlag(flag, active);

	if (flag == (1u << 4)) { // spell check toggled
		if (rules_switch) gtk_widget_set_sensitive(rules_switch, active);
		if (dicts_switch) gtk_widget_set_sensitive(dicts_switch, active);
	}
	if (active && flag == (1u << 8)) { // rules turned on → turn off dicts
		if (dicts_switch) gtk_switch_set_active(GTK_SWITCH(dicts_switch), FALSE);
	}
	if (active && flag == (1u << 9)) { // dicts turned on → turn off rules
		if (rules_switch) gtk_switch_set_active(GTK_SWITCH(rules_switch), FALSE);
	}
}

// ── Toggle row helper ──
static GtkWidget* make_toggle_row(const char *label_text, gboolean active,
	GCallback cb, guint flag)
{
	GtkWidget *row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0);
	gtk_widget_set_margin_top(row, 6); gtk_widget_set_margin_bottom(row, 6);
	GtkWidget *lbl = gtk_label_new(label_text);
	gtk_label_set_xalign(GTK_LABEL(lbl), 0);
	gtk_widget_set_hexpand(lbl, TRUE);
	GtkWidget *sw = gtk_switch_new();
	gtk_switch_set_active(GTK_SWITCH(sw), active);
	g_object_set_data(G_OBJECT(sw), "flag", GUINT_TO_POINTER(flag));
	g_signal_connect(sw, "notify::active", cb, NULL);
	gtk_box_pack_start(GTK_BOX(row), lbl, TRUE, TRUE, 0);
	gtk_box_pack_end(GTK_BOX(row), sw, FALSE, FALSE, 0);
	// Store switch ref for external access
	g_object_set_data(G_OBJECT(row), "_sw", sw);
	return row;
}

// ── Dropdown row helper ──
static GtkWidget* make_dropdown_row(const char *label_text,
	const char *items, const char *current, GCallback cb)
{
	GtkWidget *row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0);
	gtk_widget_set_margin_top(row, 6); gtk_widget_set_margin_bottom(row, 6);
	GtkWidget *lbl = gtk_label_new(label_text);
	gtk_label_set_xalign(GTK_LABEL(lbl), 0);
	gtk_widget_set_hexpand(lbl, TRUE);

	GtkWidget *combo = gtk_combo_box_text_new();
	char *copy = g_strdup(items);
	char *save, *item = strtok_r(copy, ";", &save);
	int idx = 0, active_idx = 0;
	while (item) {
		gtk_combo_box_text_append(GTK_COMBO_BOX_TEXT(combo), NULL, item);
		if (strcmp(item, current) == 0) active_idx = idx;
		idx++; item = strtok_r(NULL, ";", &save);
	}
	g_free(copy);
	gtk_combo_box_set_active(GTK_COMBO_BOX(combo), active_idx);
	g_signal_connect(combo, "changed", cb, NULL);
	gtk_box_pack_start(GTK_BOX(row), lbl, TRUE, TRUE, 0);
	gtk_box_pack_end(GTK_BOX(row), combo, FALSE, FALSE, 0);
	return row;
}

// ── Extra config format from Go: im|cs|flags|macro|cap|spell|rules|dicts|preedit|game|im_list|cs_list
static void add_other_page(GtkWidget *parent, guint ibflags, int mode, const char *extra) {
	GtkWidget *sw = gtk_scrolled_window_new(NULL, NULL);
	gtk_scrolled_window_set_policy(GTK_SCROLLED_WINDOW(sw), GTK_POLICY_NEVER, GTK_POLICY_AUTOMATIC);
	gtk_widget_set_vexpand(sw, TRUE);

	GtkWidget *vbox = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
	gtk_widget_set_margin_start(vbox, 12); gtk_widget_set_margin_end(vbox, 12);
	gtk_widget_set_margin_top(vbox, 12);

	// ── Title ──
	GtkWidget *title = gtk_label_new(NULL);
	gtk_label_set_markup(GTK_LABEL(title),
		"<span font='20' weight='bold' foreground='#2e7d32'>Kernel Telex - Hsx2Coder</span>");
	gtk_widget_set_margin_bottom(title, 2);
	gtk_box_pack_start(GTK_BOX(vbox), title, FALSE, FALSE, 0);
	if (app_version && strlen(app_version) > 0) {
		char ver[128];
		snprintf(ver, sizeof(ver), "<span font='10' foreground='#888888'>%s</span>", app_version);
		GtkWidget *subtitle = gtk_label_new(NULL);
		gtk_label_set_markup(GTK_LABEL(subtitle), ver);
		gtk_widget_set_margin_bottom(subtitle, 12);
		gtk_box_pack_start(GTK_BOX(vbox), subtitle, FALSE, FALSE, 0);
	}

	// Parse extra config
	char *current_im = NULL, *current_cs = NULL, *im_list = NULL, *cs_list = NULL;
	guint core_flags = 0, mcr=0, cap=0, spl=0, rls=0, dct=0, game=0;
	if (extra && strlen(extra) > 0) {
		char *copy = g_strdup(extra);
		char *p[13]; int n=0;
		char *save, *tok = strtok_r(copy, "|", &save);
		while (tok && n < 13) { p[n++] = tok; tok = strtok_r(NULL, "|", &save); }
		if (n >= 12) {
			current_im = g_strdup(p[0]); current_cs = g_strdup(p[1]);
			core_flags = atoi(p[2]); mcr = atoi(p[3]); cap = atoi(p[4]);
			spl = atoi(p[5]); rls = atoi(p[6]); dct = atoi(p[7]);
			game = atoi(p[9]);
			im_list = g_strdup(p[10]); cs_list = g_strdup(p[11]);
		}
		g_free(copy);
	}
	if (!current_im) current_im = g_strdup("");
	if (!current_cs) current_cs = g_strdup("");
	if (!im_list) im_list = g_strdup("");
	if (!cs_list) cs_list = g_strdup("");

	// ── Input method ──
	GtkWidget *im_row = make_dropdown_row("Kiểu gõ", im_list, current_im,
		G_CALLBACK(im_changed_cb));
	gtk_box_pack_start(GTK_BOX(vbox), im_row, FALSE, FALSE, 0);

	// ── Output charset ──
	GtkWidget *cs_row = make_dropdown_row("Bảng mã", cs_list, current_cs,
		G_CALLBACK(cs_changed_cb));
	gtk_box_pack_start(GTK_BOX(vbox), cs_row, FALSE, FALSE, 0);

	// ── Separator ──
	GtkWidget *sep = gtk_separator_new(GTK_ORIENTATION_HORIZONTAL);
	gtk_widget_set_margin_top(sep, 12); gtk_widget_set_margin_bottom(sep, 12);
	gtk_box_pack_start(GTK_BOX(vbox), sep, FALSE, FALSE, 0);

	// ── Chế độ gõ mặc định ──
	GtkWidget *mode_row = gtk_box_new(GTK_ORIENTATION_HORIZONTAL, 0);
	gtk_widget_set_margin_top(mode_row, 6); gtk_widget_set_margin_bottom(mode_row, 6);
	GtkWidget *mlbl = gtk_label_new("Chế độ gõ mặc định");
	gtk_label_set_xalign(GTK_LABEL(mlbl), 0);
	gtk_widget_set_hexpand(mlbl, TRUE);
	GtkWidget *mcombo = make_dropdown(mode);
	gtk_box_pack_start(GTK_BOX(mode_row), mlbl, TRUE, TRUE, 0);
	gtk_box_pack_end(GTK_BOX(mode_row), mcombo, FALSE, FALSE, 0);
	gtk_box_pack_start(GTK_BOX(vbox), mode_row, FALSE, FALSE, 0);

	// ── Core flags ──
	sep = gtk_separator_new(GTK_ORIENTATION_HORIZONTAL);
	gtk_widget_set_margin_top(sep, 12); gtk_widget_set_margin_bottom(sep, 12);
	gtk_box_pack_start(GTK_BOX(vbox), sep, FALSE, FALSE, 0);

	struct { char *label; guint flag; } core_toggles[] = {
		{"Bỏ dấu tự do", 1},       // EfreeToneMarking
		{"Dấu thanh chuẩn", 2},    // EstdToneStyle
	};
	for (int i = 0; i < 2; i++) {
		gtk_box_pack_start(GTK_BOX(vbox),
			make_toggle_row(core_toggles[i].label,
				(core_flags & core_toggles[i].flag) != 0,
				G_CALLBACK(core_flag_cb), core_toggles[i].flag),
			FALSE, FALSE, 0);
	}

	// ── IB toggles ──
	sep = gtk_separator_new(GTK_ORIENTATION_HORIZONTAL);
	gtk_widget_set_margin_top(sep, 12); gtk_widget_set_margin_bottom(sep, 12);
	gtk_box_pack_start(GTK_BOX(vbox), sep, FALSE, FALSE, 0);

	struct { char *label; guint bit; } ib_toggles[] = {
		{"Bật gõ tắt", 1},
		{"Tự động viết hoa", 18},
		{"Kiểm tra chính tả", 4},
		{"Sử dụng luật ghép vần", 8},
		{"Sử dụng từ điển", 9},
	};
	int ib_vals[] = {mcr, cap, spl, rls, dct};
	for (int i = 0; i < 5; i++) {
		GtkWidget *row = make_toggle_row(ib_toggles[i].label, ib_vals[i],
			G_CALLBACK(ib_flag_cb), 1u << ib_toggles[i].bit);
		gtk_box_pack_start(GTK_BOX(vbox), row, FALSE, FALSE, 0);
		GtkWidget *sw = g_object_get_data(G_OBJECT(row), "_sw");
		if (ib_toggles[i].bit == 4) spell_switch = sw;
		if (ib_toggles[i].bit == 8) rules_switch = sw;
		if (ib_toggles[i].bit == 9) dicts_switch = sw;
	}
	// Initial state: rules/dicts depend on spell check
	if (spell_switch && !gtk_switch_get_active(GTK_SWITCH(spell_switch))) {
		if (rules_switch) gtk_widget_set_sensitive(rules_switch, FALSE);
		if (dicts_switch) gtk_widget_set_sensitive(dicts_switch, FALSE);
	}

	// ── Game toggle ──
	gtk_box_pack_start(GTK_BOX(vbox),
		make_toggle_row("Tắt trong game",
			game,
			G_CALLBACK(ib_flag_cb), IBdisableOnGame),
		FALSE, FALSE, 0);

	gtk_container_add(GTK_CONTAINER(sw), vbox);
	gtk_box_pack_start(GTK_BOX(parent), sw, TRUE, TRUE, 0);

	g_free(current_im); g_free(current_cs);
	g_free(im_list); g_free(cs_list);
}

int openGUI(guint flags, int mode, guint32 *s, int size,
	char *mtext, char *cfg_text, char *app_mappings, char *extra_config)
{
	key_pairs_tmp = s;
	app_version = cfg_text ? cfg_text : "";
	gtk_init(NULL, NULL);

	GtkWidget *win = gtk_window_new(GTK_WINDOW_TOPLEVEL);
	gtk_window_set_title(GTK_WINDOW(win), "KTelex Settings");
	gtk_window_set_default_size(GTK_WINDOW(win), 620, 600);
	gtk_window_set_position(GTK_WINDOW(win), GTK_WIN_POS_CENTER);
	g_signal_connect(win, "destroy", G_CALLBACK(gtk_main_quit), NULL);

	GtkWidget *header = gtk_header_bar_new();
	gtk_header_bar_set_show_close_button(GTK_HEADER_BAR(header), TRUE);
	gtk_window_set_titlebar(GTK_WINDOW(win), header);

	stack = gtk_stack_new();
	gtk_stack_set_transition_type(GTK_STACK(stack), GTK_STACK_TRANSITION_TYPE_SLIDE_LEFT_RIGHT);
	gtk_stack_set_transition_duration(GTK_STACK(stack), 150);
	GtkWidget *switcher = gtk_stack_switcher_new();
	gtk_stack_switcher_set_stack(GTK_STACK_SWITCHER(switcher), GTK_STACK(stack));
	gtk_header_bar_set_custom_title(GTK_HEADER_BAR(header), switcher);

	GtkWidget *page;
	const char *map = app_mappings ? app_mappings : "";

	page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
	gtk_widget_set_vexpand(page, TRUE);
	add_other_page(page, flags, mode, extra_config ? extra_config : "");
	gtk_stack_add_titled(GTK_STACK(stack), page, "general", "General");

	page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
	gtk_widget_set_vexpand(page, TRUE);
	add_shortcut_page(page, win);
	gtk_stack_add_titled(GTK_STACK(stack), page, "shortcuts", "Hotkey");

	page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
	gtk_widget_set_vexpand(page, TRUE);
	add_typing_page(page, win, mtext);
	gtk_stack_add_titled(GTK_STACK(stack), page, "typing", "Typing");

	page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
	gtk_widget_set_vexpand(page, TRUE);
	add_app_list_page(page, map, 0, "WM_CLASS (ví dụ: firefox)");
	gtk_stack_add_titled(GTK_STACK(stack), page, "apps", "Apps");

	page = gtk_box_new(GTK_ORIENTATION_VERTICAL, 0);
	gtk_widget_set_vexpand(page, TRUE);
	add_app_list_page(page, map, 3, "WM_CLASS (ví dụ: chrome)");
	gtk_stack_add_titled(GTK_STACK(stack), page, "ignore", "Ignore");

	gtk_container_add(GTK_CONTAINER(win), stack);
	gtk_widget_show_all(win);
	gtk_main();
	return 0;
}
