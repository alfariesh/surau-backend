#!/usr/bin/env python3
"""Versioned LangExtract prompts for classical Arabic knowledge extraction."""

from __future__ import annotations

from dataclasses import dataclass
import hashlib
import json
import textwrap
from typing import Any

import langextract as lx


@dataclass(frozen=True)
class PromptSpec:
    task: str
    version: str
    description: str
    examples: list[Any]
    extraction_classes: tuple[str, ...]

    @property
    def policy_hash(self) -> str:
        payload = {
            "task": self.task,
            "version": self.version,
            "description": self.description,
            "extraction_classes": self.extraction_classes,
            "examples": [
                {
                    "text": example.text,
                    "extractions": [
                        {
                            "class": extraction.extraction_class,
                            "text": extraction.extraction_text,
                            "attributes": extraction.attributes or {},
                        }
                        for extraction in example.extractions
                    ],
                }
                for example in self.examples
            ],
        }
        return hashlib.sha256(json.dumps(payload, ensure_ascii=False, sort_keys=True).encode("utf-8")).hexdigest()


MENTION_CLASSES = ("person", "person_reference", "theonym", "place", "work_title", "group", "institution")
TERM_CLASSES = (
    "concept",
    "fiqh_term",
    "aqidah_term",
    "hadith_term",
    "qiraat_term",
    "arabic_language_term",
    "adab_term",
    "tasawwuf_term",
)
CITATION_CLASSES = (
    "quran_reference",
    "hadith_reference",
    "athar",
    "quote",
    "poetry",
    "book_reference",
    "isnad_chain",
)
RELATION_CLASSES = ("relation", "claim")


def get_prompt(task: str) -> PromptSpec:
    task = task.strip().lower()
    if task == "mentions":
        return mentions_v1()
    if task == "terms":
        return terms_v1()
    if task == "citations":
        return citations_v1()
    if task == "relations":
        return relations_v1()
    raise ValueError(f"unknown extraction task: {task}")


def mentions_v1() -> PromptSpec:
    description = textwrap.dedent(
        """
        استخرج الأعلام الصريحة من نص عربي تراثي لاستخدامها في فهرس معرفي موثق.

        القواعد:
        1. يجب أن يكون extraction_text نصًا حرفيًا موجودًا في المدخل، بلا إعادة صياغة.
        2. لا تستخرج شيئًا من معرفتك العامة إذا لم يرد في النص.
        3. لا تستخرج الضمائر أو أفعال القول المفردة مثل: هو، قال، قيل.
        4. لا تستخرج الألقاب العامة وحدها مثل: الشيخ، الإمام، العلامة.
        5. استخرج الكيان بترتيب ظهوره، وتجنب التداخل بين النصوص المستخرجة.
        6. إذا كان الاسم قصيرًا أو يحتمل أكثر من شخص، ضع certainty = ambiguous.
        7. صنف أسماء الله تعالى المستقلة مثل: الله، اللهم، الرب كـ theonym لا كـ person أو person_reference.
        8. صنف المراجع اللقبية مثل: النبي، رسول الله كـ person_reference لا كـ person.
        9. صنف أسماء الكتب والمؤلفات كـ work_title، ولا تصنف السور القرآنية ككتب.

        الأنواع المطلوبة:
        person, person_reference, theonym, place, work_title, group, institution
        """
    ).strip()

    examples = [
        lx.data.ExampleData(
            text=(
                "سافر أبو حامد الغزالي إلى بغداد وذكر كتاب إحياء علوم الدين "
                "لأهل المدرسة النظامية."
            ),
            extractions=[
                lx.data.Extraction(
                    extraction_class="person",
                    extraction_text="أبو حامد الغزالي",
                    attributes={
                        "name_form": "kunya_and_laqab",
                        "role_hint": "scholar",
                        "certainty": "explicit",
                        "needs_review": "no",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="place",
                    extraction_text="بغداد",
                    attributes={"certainty": "explicit", "needs_review": "no"},
                ),
                lx.data.Extraction(
                    extraction_class="work_title",
                    extraction_text="إحياء علوم الدين",
                    attributes={"certainty": "explicit", "needs_review": "no"},
                ),
                lx.data.Extraction(
                    extraction_class="institution",
                    extraction_text="المدرسة النظامية",
                    attributes={"certainty": "explicit", "needs_review": "no"},
                ),
            ],
        ),
        lx.data.ExampleData(
            text="وقال أحمد: سمعت محمدا يذكر أهل الحديث في مكة.",
            extractions=[
                lx.data.Extraction(
                    extraction_class="person",
                    extraction_text="أحمد",
                    attributes={
                        "name_form": "ism",
                        "role_hint": "unknown",
                        "certainty": "ambiguous",
                        "needs_review": "yes",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="person",
                    extraction_text="محمدا",
                    attributes={
                        "name_form": "ism",
                        "role_hint": "unknown",
                        "certainty": "ambiguous",
                        "needs_review": "yes",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="group",
                    extraction_text="أهل الحديث",
                    attributes={"certainty": "explicit", "needs_review": "no"},
                ),
                lx.data.Extraction(
                    extraction_class="place",
                    extraction_text="مكة",
                    attributes={"certainty": "explicit", "needs_review": "no"},
                ),
            ],
        ),
        lx.data.ExampleData(
            text="دعا المصنف: اللهم اغفر لنا، وقال رسول الله صلى الله عليه وسلّم كما في صحيح البخاري.",
            extractions=[
                lx.data.Extraction(
                    extraction_class="theonym",
                    extraction_text="اللهم",
                    attributes={
                        "name_form": "divine_name",
                        "role_hint": "theonym",
                        "certainty": "explicit",
                        "needs_review": "no",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="person_reference",
                    extraction_text="رسول الله صلى الله عليه وسلّم",
                    attributes={
                        "name_form": "title",
                        "role_hint": "prophet_reference",
                        "certainty": "explicit",
                        "needs_review": "yes",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="work_title",
                    extraction_text="صحيح البخاري",
                    attributes={"certainty": "explicit", "needs_review": "no"},
                ),
            ],
        ),
    ]
    return PromptSpec("mentions", "mentions_v2", description, examples, MENTION_CLASSES)


def terms_v1() -> PromptSpec:
    description = textwrap.dedent(
        """
        استخرج المصطلحات العلمية والمفاهيم الإسلامية الصريحة من النص.

        القواعد:
        1. extraction_text يجب أن يكون نصًا حرفيًا من المدخل.
        2. لا تستخرج مفاهيم بالاستنباط البعيد؛ استخرج ما يذكره النص صراحة.
        3. ميّز بين مصطلحات القراءات والحديث والفقه والعقيدة واللغة والأدب والتزكية.
        4. إذا كان اللفظ عامًا جدًا وليس مصطلحًا في السياق فلا تستخرجه.
        5. ضع teaching_type إذا كان السياق تعريفًا أو تحذيرًا أو تصنيفًا.
        6. qiraat_term لمصطلحات القراءات ورسم المصحف وضبط الوجوه مثل: علل القراءات، القراءات العشر، القراءة المتواترة، رسم المصحف، الشذوذ.
        7. arabic_language_term لمصطلحات النحو والإعراب والصرف واللغة، ولا تجعلها hadith_term.
        8. hadith_term خاص بمصطلحات الحديث والإسناد والمتن والرواة في سياق الحديث.

        الأنواع المطلوبة:
        concept, fiqh_term, aqidah_term, hadith_term, qiraat_term,
        arabic_language_term, adab_term, tasawwuf_term
        """
    ).strip()

    examples = [
        lx.data.ExampleData(
            text="يعنى علم علل القراءات بتخريج القراءات العشر وموافقة رسم المصحف، ويبحث في النحو.",
            extractions=[
                lx.data.Extraction(
                    extraction_class="qiraat_term",
                    extraction_text="علل القراءات",
                    attributes={
                        "domain": "qiraat",
                        "is_definition": "yes",
                        "teaching_type": "definition",
                        "certainty": "explicit",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="qiraat_term",
                    extraction_text="القراءات العشر",
                    attributes={
                        "domain": "qiraat",
                        "is_definition": "no",
                        "teaching_type": "statement",
                        "certainty": "explicit",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="qiraat_term",
                    extraction_text="رسم المصحف",
                    attributes={
                        "domain": "qiraat",
                        "is_definition": "no",
                        "teaching_type": "condition",
                        "certainty": "explicit",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="arabic_language_term",
                    extraction_text="النحو",
                    attributes={
                        "domain": "arabic_language",
                        "is_definition": "no",
                        "teaching_type": "condition",
                        "certainty": "explicit",
                    },
                ),
            ],
        ),
        lx.data.ExampleData(
            text="قال المصنف إن الإخلاص أصل العمل، وأن الرياء يفسد النية في العبادة.",
            extractions=[
                lx.data.Extraction(
                    extraction_class="tasawwuf_term",
                    extraction_text="الإخلاص",
                    attributes={
                        "domain": "adab_tazkiyah",
                        "is_definition": "no",
                        "teaching_type": "statement",
                        "certainty": "explicit",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="tasawwuf_term",
                    extraction_text="الرياء",
                    attributes={
                        "domain": "adab_tazkiyah",
                        "is_definition": "no",
                        "teaching_type": "warning",
                        "certainty": "explicit",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="concept",
                    extraction_text="النية",
                    attributes={
                        "domain": "adab_tazkiyah",
                        "is_definition": "no",
                        "teaching_type": "statement",
                        "certainty": "explicit",
                    },
                ),
            ],
        ),
        lx.data.ExampleData(
            text="الحديث الصحيح ما اتصل سنده بنقل العدل الضابط عن مثله.",
            extractions=[
                lx.data.Extraction(
                    extraction_class="hadith_term",
                    extraction_text="الحديث الصحيح",
                    attributes={
                        "domain": "hadith",
                        "is_definition": "yes",
                        "teaching_type": "definition",
                        "certainty": "explicit",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="hadith_term",
                    extraction_text="سنده",
                    attributes={
                        "domain": "hadith",
                        "is_definition": "no",
                        "teaching_type": "definition",
                        "certainty": "explicit",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="hadith_term",
                    extraction_text="العدل الضابط",
                    attributes={
                        "domain": "hadith",
                        "is_definition": "no",
                        "teaching_type": "definition",
                        "certainty": "explicit",
                    },
                ),
            ],
        ),
    ]
    return PromptSpec("terms", "terms_v2", description, examples, TERM_CLASSES)


def citations_v1() -> PromptSpec:
    description = textwrap.dedent(
        """
        استخرج الإحالات والنقول الصريحة من النص العربي التراثي.

        القواعد:
        1. extraction_text يجب أن يكون نصًا حرفيًا موجودًا في المدخل.
        2. لا تخترع موضع آية أو حديث إذا لم يذكره النص.
        3. استخرج مواضع القرآن والأحاديث والآثار والشعر والنقول وذكر الكتب.
        4. quran_reference للإحالة أو الموضع مثل: سورة الإخلاص، [الحجرات: 10]، وليس لمجرد نص آية بلا موضع.
        5. نص الآية أو الحديث يكون quote أو hadith_reference مع locator_text إذا ذكر المصدر.
        6. لا تستخرج الصيغ التعبدية العامة مثل البسملة والحمدلة والصلاة على النبي كـ quote أو quran_reference إلا إذا ذكر النص موضعها صراحة.
        7. إذا كانت الإحالة غير صريحة فضع citation_certainty = ambiguous.
        8. book_reference يكون لعنوان كتاب أو مجموعة حديثية صريحة، لا لاسم مؤلف أو شاعر مجرد.
        9. إذا نسب النص شعرًا أو قولًا إلى شخص مثل ابن الجزري، فاستخرج الشعر أو القول واجعل اسم الشخص في locator_text أو attributed_to، ولا تستخرجه book_reference.

        الأنواع المطلوبة:
        quran_reference, hadith_reference, athar, quote, poetry, book_reference,
        isnad_chain
        """
    ).strip()

    examples = [
        lx.data.ExampleData(
            text=(
                "قال الله تعالى في سورة الإخلاص: {قل هو الله أحد}. وروى البخاري: "
                "إنما الأعمال بالنيات. وأنشد الشاعر: العلم نور."
            ),
            extractions=[
                lx.data.Extraction(
                    extraction_class="quran_reference",
                    extraction_text="سورة الإخلاص",
                    attributes={
                        "reference_type": "quran",
                        "locator_text": "surah",
                        "citation_certainty": "explicit",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="quote",
                    extraction_text="قل هو الله أحد",
                    attributes={
                        "reference_type": "quran_quote",
                        "locator_text": "سورة الإخلاص",
                        "citation_certainty": "explicit",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="book_reference",
                    extraction_text="البخاري",
                    attributes={
                        "reference_type": "hadith_collection",
                        "locator_text": "unknown",
                        "citation_certainty": "explicit",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="hadith_reference",
                    extraction_text="إنما الأعمال بالنيات",
                    attributes={
                        "reference_type": "hadith",
                        "locator_text": "البخاري",
                        "citation_certainty": "explicit",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="poetry",
                    extraction_text="العلم نور",
                    attributes={
                        "reference_type": "poetry",
                        "locator_text": "unknown",
                        "citation_certainty": "explicit",
                    },
                ),
            ],
        ),
        lx.data.ExampleData(
            text="افتتح بقوله: بسم الله الرحمن الرحيم، ثم أحال إلى صحيح مسلم.",
            extractions=[
                lx.data.Extraction(
                    extraction_class="book_reference",
                    extraction_text="صحيح مسلم",
                    attributes={
                        "reference_type": "book",
                        "locator_text": "unknown",
                        "citation_certainty": "explicit",
                    },
                ),
            ],
        ),
        lx.data.ExampleData(
            text="وذكره صاحب الرسالة في باب الطهارة، ثم قال: الطهور شطر الإيمان.",
            extractions=[
                lx.data.Extraction(
                    extraction_class="book_reference",
                    extraction_text="الرسالة",
                    attributes={
                        "reference_type": "book",
                        "locator_text": "باب الطهارة",
                        "citation_certainty": "explicit",
                    },
                ),
                lx.data.Extraction(
                    extraction_class="quote",
                    extraction_text="الطهور شطر الإيمان",
                    attributes={
                        "reference_type": "quote",
                        "locator_text": "unknown",
                        "citation_certainty": "explicit",
                    },
                ),
            ],
        ),
        lx.data.ExampleData(
            text="وقد جمعها ابن الجزري في قوله: فكل ما وافق وجه نحو ... وصح إسنادا هو القرآن.",
            extractions=[
                lx.data.Extraction(
                    extraction_class="poetry",
                    extraction_text="فكل ما وافق وجه نحو ... وصح إسنادا هو القرآن",
                    attributes={
                        "reference_type": "poetry",
                        "locator_text": "ابن الجزري",
                        "attributed_to": "ابن الجزري",
                        "citation_certainty": "explicit",
                    },
                ),
            ],
        ),
    ]
    return PromptSpec("citations", "citations_v3", description, examples, CITATION_CLASSES)


def relations_v1() -> PromptSpec:
    description = textwrap.dedent(
        """
        استخرج العلاقات والادعاءات الصريحة فقط من النص.

        القواعد:
        1. هذه المهمة عالية المخاطر؛ لا تستخرج إلا ما يدل عليه النص بوضوح.
        2. extraction_text يجب أن يكون العبارة الدالة حرفيًا من النص.
        3. لا تستعمل المعرفة العامة ولا تكمل الأطراف الناقصة من خارج النص.
        4. كل علاقة أو claim يجب أن يبقى review_status = needs_review.
        """
    ).strip()

    examples = [
        lx.data.ExampleData(
            text="صنف أبو حامد الغزالي كتاب إحياء علوم الدين.",
            extractions=[
                lx.data.Extraction(
                    extraction_class="relation",
                    extraction_text="صنف أبو حامد الغزالي كتاب إحياء علوم الدين",
                    attributes={
                        "predicate": "authored_by",
                        "subject_text": "إحياء علوم الدين",
                        "object_text": "أبو حامد الغزالي",
                        "certainty": "explicit",
                        "requires_scholar_review": "yes",
                    },
                ),
            ],
        )
    ]
    return PromptSpec("relations", "relations_v1", description, examples, RELATION_CLASSES)
