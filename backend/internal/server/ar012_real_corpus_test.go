package server

// ar012RealPaper is one real open-access work (OpenAlex metadata: real
// title, authors, year, venue, DOI, abstract) used as genuine frozen
// evidence for the AR-012 blind-evaluation batch. Metadata is factual
// public bibliographic data; abstracts are the publishers' own text.
type ar012RealPaper struct {
	Title    string
	Authors  []string
	Year     int64
	Venue    string
	DOI      string
	Abstract string
}

// ar012RealCorpus holds the three subject pools (7-8 works each). Case axes
// take the first N papers and truncate abstracts at the length knob.
var ar012RealCorpus = map[string][]ar012RealPaper{
	"governance": {
		{
			Title:    "Performance of ChatGPT on USMLE: Potential for AI-assisted medical education using large language models",
			Authors:  []string{"Tiffany H. Kung", "Morgan Cheatham", "Arielle Medenilla", "Czarina Sillos"},
			Year:     2023,
			Venue:    "PLOS Digital Health",
			DOI:      "10.1371/journal.pdig.0000198",
			Abstract: "We evaluated the performance of a large language model called ChatGPT on the United States Medical Licensing Exam (USMLE), which consists of three exams: Step 1, Step 2CK, and Step 3. ChatGPT performed at or near the passing threshold for all three exams without any specialized training or reinforcement. Additionally, ChatGPT demonstrated a high level of concordance and insight in its explanations. These results suggest that large language models may have the potential to assist with medical education, and potentially, clinical decision-making.",
		},
		{
			Title:    "A survey on large language model based autonomous agents",
			Authors:  []string{"Lei Wang", "Chen Ma", "Xueyang Feng", "Zeyu Zhang"},
			Year:     2024,
			Venue:    "Frontiers of Computer Science",
			DOI:      "10.1007/s11704-024-40231-1",
			Abstract: "Abstract Autonomous agents have long been a research focus in academic and industry communities. Previous research often focuses on training agents with limited knowledge within isolated environments, which diverges significantly from human learning processes, and makes the agents hard to achieve human-like decisions. Recently, through the acquisition of vast amounts of Web knowledge, large language models (LLMs) have shown potential in human-level intelligence, leading to a surge in research on LLM-based autonomous agents. In this paper, we present a comprehensive survey of these studies, delivering a systematic review of LLM-based autonomous agents from a holistic perspective. We first discuss the construction of LLM-based autonomous agents, proposing a unified framework that encompasses much of previous work. Then, we present a overview of the diverse applications of LLM-based autonomous agents in social science, natural science, and engineering. Finally, we delve into the evaluation strategies commonly used for LLM-based autonomous agents. Based on the previous studies, we also present several challenges and future directions in this field.",
		},
		{
			Title:    "Academic Integrity considerations of AI Large Language Models in the post-pandemic era: ChatGPT and beyond",
			Authors:  []string{"Mike Perkins"},
			Year:     2023,
			Venue:    "Journal of University Teaching and Learning Practice",
			DOI:      "10.53761/1.20.02.07",
			Abstract: "This paper explores the academic integrity considerations of students’ use of Artificial Intelligence (AI) tools using Large Language Models (LLMs) such as ChatGPT in formal assessments. We examine the evolution of these tools, and highlight the potential ways that LLMs can support in the education of students in digital writing and beyond, including the teaching of writing and composition, the possibilities of co-creation between humans and AI, supporting EFL learners, and improving Automated Writing Evaluations (AWE). We describe and demonstrate the potential that these tools have in creating original, coherent text that can avoid detection by existing technological methods of detection and trained academic staff alike, demonstrating a major academic integrity concern related to the use of these tools by students. Analysing the various issues related to academic integrity that LLMs raise for both Higher Education Institutions (HEIs) and students, we conclude that it is not the student use of any AI tools that defines whether plagiarism or a breach of academic integrity has occurred, but whether any use is made clear by the student. Deciding whether any particular use of LLMs by students can be defined as academic misconduct is determined by the academic integrity policies of any given HEI, which must be updated to consider how these tools will be used in future educational en",
		},
		{
			Title:    "Large Language Models in Medical Education: Opportunities, Challenges, and Future Directions",
			Authors:  []string{"Alaa Abd‐Alrazaq", "Rawan AlSaad", "Dari Alhuwail", "Arfan Ahmed"},
			Year:     2023,
			Venue:    "JMIR Medical Education",
			DOI:      "10.2196/48291",
			Abstract: "The integration of large language models (LLMs), such as those in the Generative Pre-trained Transformers (GPT) series, into medical education has the potential to transform learning experiences for students and elevate their knowledge, skills, and competence. Drawing on a wealth of professional and academic experience, we propose that LLMs hold promise for revolutionizing medical curriculum development, teaching methodologies, personalized study plans and learning materials, student assessments, and more. However, we also critically examine the challenges that such integration might pose by addressing issues of algorithmic bias, overreliance, plagiarism, misinformation, inequity, privacy, and copyright concerns in medical education. As we navigate the shift from an information-driven educational paradigm to an artificial intelligence (AI)-driven educational paradigm, we argue that it is paramount to understand both the potential and the pitfalls of LLMs in medical education. This paper thus offers our perspective on the opportunities and challenges of using LLMs in this context. We believe that the insights gleaned from this analysis will serve as a foundation for future recommendations and best practices in the field, fostering the responsible and effective use of AI technologies in medical education.",
		},
		{
			Title:    "From human writing to artificial intelligence generated text: examining the prospects and potential threats of ChatGPT in academic writing",
			Authors:  []string{"Ismail Dergaa", "Karim Chamari", "Piotr Żmijewski", "Helmi Ben Saad"},
			Year:     2023,
			Venue:    "Biology of Sport",
			DOI:      "10.5114/biolsport.2023.125623",
			Abstract: "Natural language processing (NLP) has been studied in computing for decades. Recent technological advancements have led to the development of sophisticated artificial intelligence (AI) models, such as Chat Generative Pre-trained Transformer (ChatGPT). These models can perform a range of language tasks and generate human-like responses, which offers exciting prospects for academic efficiency. This manuscript aims at (i) exploring the potential benefits and threats of ChatGPT and other NLP technologies in academic writing and research publications; (ii) highlights the ethical considerations involved in using these tools, and (iii) consider the impact they may have on the authenticity and credibility of academic work. This study involved a literature review of relevant scholarly articles published in peer-reviewed journals indexed in Scopus as quartile 1. The search used keywords such as \"ChatGPT,\" \"AI-generated text,\" \"academic writing,\" and \"natural language processing.\" The analysis was carried out using a quasi-qualitative approach, which involved reading and critically evaluating the sources and identifying relevant data to support the research questions. The study found that ChatGPT and other NLP technologies have the potential to enhance academic writing and research efficiency. However, their use also raises concerns about the impact on the authenticity and credibility of ",
		},
		{
			Title:    "Artificial Hallucinations in ChatGPT: Implications in Scientific Writing",
			Authors:  []string{"Hussam Alkaissi", "Samy I. McFarlane"},
			Year:     2023,
			Venue:    "Cureus",
			DOI:      "10.7759/cureus.35179",
			Abstract: "While still in its infancy, ChatGPT (Generative Pretrained Transformer), introduced in November 2022, is bound to hugely impact many industries, including healthcare, medical education, biomedical research, and scientific writing. Implications of ChatGPT, that new chatbot introduced by OpenAI on academic writing, is largely unknown. In response to the Journal of Medical Science (Cureus) Turing Test - call for case reports written with the assistance of ChatGPT, we present two cases one of homocystinuria-associated osteoporosis, and the other is on late-onset Pompe disease (LOPD), a rare metabolic disorder. We tested ChatGPT to write about the pathogenesis of these conditions. We documented the positive, negative, and rather troubling aspects of our newly introduced chatbot's performance.",
		},
		{
			Title:    "Students’ voices on generative AI: perceptions, benefits, and challenges in higher education",
			Authors:  []string{"Cecilia Ka Yuk Chan", "Wenjie Hu"},
			Year:     2023,
			Venue:    "International Journal of Educational Technology in Higher Education",
			DOI:      "10.1186/s41239-023-00411-8",
			Abstract: "Abstract This study explores university students’ perceptions of generative AI (GenAI) technologies, such as ChatGPT, in higher education, focusing on familiarity, their willingness to engage, potential benefits and challenges, and effective integration. A survey of 399 undergraduate and postgraduate students across diverse disciplines revealed that postgraduate students, females, and those in non-STEM programmes had higher familiarity with GenAI. Students generally acknowledged the benefits of GenAI in preparing for exams, writing assistance, and brainstorming. However, they expressed concerns about accuracy, privacy, and ethical issues, and demonstrated a varying willingness to engage with GenAI in various scenarios. The findings underscore the need for tailored integration strategies and comprehensive support and training to maximise the benefits of GenAI. This study offers recommendations for policy, guidelines, and strategic plans around GenAI in higher education.",
		},
	},
	"policy": {
		{
			Title:    "Recent Developments in the Econometrics of Program Evaluation",
			Authors:  []string{"Guido W. Imbens", "Jeffrey M. Wooldridge"},
			Year:     2009,
			Venue:    "Journal of Economic Literature",
			DOI:      "10.1257/jel.47.1.5",
			Abstract: "Many empirical questions in economics and other social sciences depend on causal effects of programs or policies. In the last two decades, much research has been done on the econometric and statistical analysis of such causal effects. This recent theoretical literature has built on, and combined features of, earlier work in both the statistics and econometrics literatures. It has by now reached a level of maturity that makes it an important tool in many areas of empirical research in economics, including labor economics, public finance, development economics, industrial organization, and other areas of empirical microeconomics. In this review, we discuss some of the recent developments. We focus primarily on practical issues for empirical researchers, as well as provide a historical overview of the area and give references to more technical research.",
		},
		{
			Title:    "Modes of Network Governance: Structure, Management, and Effectiveness",
			Authors:  []string{"Keith G. Provan", "Patrick Kenis"},
			Year:     2007,
			Venue:    "Journal of Public Administration Research and Theory",
			DOI:      "10.1093/jopart/mum015",
			Abstract: "This article examines the governance of organizational networks and the impact of governance on network effectiveness. Three basic models, or forms, of network governance are developed focusing on their distinct structural properties. Propositions are formulated examining conditions for the effectiveness of each form. The tensions inherent in each form are then discussed, followed by the role that management may play in addressing these tensions. Finally, the evolution of governance is explored.",
		},
		{
			Title:    "Effects of a Classroom-Based Program on Physical Activity and On-Task Behavior",
			Authors:  []string{"Matthew T. Mahar", "Sheila K. Murphy", "David A. Rowe", "Jeannie A. Golden"},
			Year:     2006,
			Venue:    "Medicine & Science in Sports & Exercise",
			DOI:      "10.1249/01.mss.0000235359.16685.a3",
			Abstract: "PURPOSE: This study evaluated the effects of a classroom-based physical activity program on children's in-school physical activity levels and on-task behavior during academic instruction. METHODS: Physical activity of 243 students was assessed during school hours. Intervention-group students (N = 135) received a classroom-based program (i.e., Energizers). The control group (N = 108) did not receive Energizers. On-task behavior during academic instruction time was observed for 62 third-grade (N = 37) and fourth-grade students (N = 25) before and after Energizers activities. An independent groups t-test compared in-school physical activity levels between intervention and control classes. A multiple-baseline across-classrooms design was used to evaluate the effectiveness of the Energizers on on-task behavior. Additionally, a two-way (time [pre- vs postobservation] x period [baseline vs intervention]) repeated-measures analysis of variance compared on-task behavior between observation periods. Magnitudes of mean differences were evaluated with Cohen's delta (ES). RESULTS: Students in the intervention group took significantly (P < 0.05) more in-school steps (5587 +/- 1633) than control-group students (4805 +/- 1543), and the size of this difference was moderate (ES = 0.49). The intervention was effective in improving on-task behavior; after the Energizers were systematically impleme",
		},
		{
			Title:    "Recommendations for Conduct, Methodological Practices, and Reporting of Cost-effectiveness Analyses",
			Authors:  []string{"Gillian D Sanders", "Peter J. Neumann", "Anirban Basu", "Dan W. Brock"},
			Year:     2016,
			Venue:    "JAMA",
			DOI:      "10.1001/jama.2016.12195",
			Abstract: "IMPORTANCE: Since publication of the report by the Panel on Cost-Effectiveness in Health and Medicine in 1996, researchers have advanced the methods of cost-effectiveness analysis, and policy makers have experimented with its application. The need to deliver health care efficiently and the importance of using analytic techniques to understand the clinical and economic consequences of strategies to improve health have increased in recent years. OBJECTIVE: To review the state of the field and provide recommendations to improve the quality of cost-effectiveness analyses. The intended audiences include researchers, government policy makers, public health officials, health care administrators, payers, businesses, clinicians, patients, and consumers. DESIGN: In 2012, the Second Panel on Cost-Effectiveness in Health and Medicine was formed and included 2 co-chairs, 13 members, and 3 additional members of a leadership group. These members were selected on the basis of their experience in the field to provide broad expertise in the design, conduct, and use of cost-effectiveness analyses. Over the next 3.5 years, the panel developed recommendations by consensus. These recommendations were then reviewed by invited external reviewers and through a public posting process. FINDINGS: The concept of a \"reference case\" and a set of standard methodological practices that all cost-effectiveness a",
		},
		{
			Title:    "Evaluating the SOSsuicide prevention program: a replication and extension",
			Authors:  []string{"Robert H. Aseltine", "Amy James", "Elizabeth A. Schilling", "Jaime L. Glanovsky"},
			Year:     2007,
			Venue:    "BMC Public Health",
			DOI:      "10.1186/1471-2458-7-161",
			Abstract: "BACKGROUND: Suicide is a leading cause of death for children and youth in the United States. Although school based programs have been the principal vehicle for youth suicide prevention efforts for over two decades, few have been systematically evaluated. This study examined the effectiveness of the Signs of Suicide (SOS) prevention program in reducing suicidal behavior. METHODS: 4133 students in 9 high schools in Columbus, Georgia, western Massachusetts, and Hartford, Connecticut were randomly assigned to intervention and control groups during the 2001-02 and 2002-03 school years. Self-administered questionnaires were completed by students in both groups approximately 3 months after program implementation. RESULTS: Significantly lower rates of suicide attempts and greater knowledge and more adaptive attitudes about depression and suicide were observed among students in the intervention group. Students' race/ethnicity, grade, and gender did not alter the impact of the intervention on any of the outcomes assessed in this analysis. CONCLUSION: This study has confirmed preliminary analysis of Year 1 data with a larger and more racially and socio-economically diverse sample. SOS continues to be the only universal school-based suicide prevention program to demonstrate significant effects of self-reported suicide attempts in a study utilizing a randomized experimental design. Moreover",
		},
		{
			Title:    "School-Based Programs to Reduce Bullying and Victimization",
			Authors:  []string{"David P. Farrington", "Maria M. Ttofi"},
			Year:     2009,
			Venue:    "Campbell Systematic Reviews",
			DOI:      "10.1037/e528362010-001",
			Abstract: "Bullying is becoming an ever more pressing issue for schools, daycare centers, politicians and the public. Everyone agrees that bullying is a serious problem and initiatives are urgently called for to stamp it out. This Campbell Systematic Review studied the effects of anti-bullying programs in schools. The conclusion is that programs generally work and bullying is reduced on average by around 20%. A total of 89 reports were of sufficient quality to be included in the systematic review. The 89 reports describe 53 different studies. However, nine studies did not provide enough data to allow the calculation of an effect size and were, therefore, not included in the final meta-analysis. The overall analysis is therefore based on a total of 44 studies. The 44 different studies were carried out between 1983 and mid-2009 and came from 16 different countries. The included studies were either randomized controlled trials, quasi-randomized trials, age-cohort studies or other controlled studies. Furthermore, the systematic review clearly states that future evaluations should measure the children's situation before and after an anti-bullying program. This should apply to the experimental group as well as the control group to get the most accurate results possible. Executive Summary/Abstract BACKGROUND School bullying has serious short-term and long-term effects on children's physical and ",
		},
		{
			Title:    "Understanding and misunderstanding randomized controlled trials",
			Authors:  []string{"Angus Deaton", "Nancy Cartwright"},
			Year:     2017,
			Venue:    "Social Science & Medicine",
			DOI:      "10.1016/j.socscimed.2017.12.005",
			Abstract: "Randomized Controlled Trials (RCTs) are increasingly popular in the social sciences, not only in medicine. We argue that the lay public, and sometimes researchers, put too much trust in RCTs over other methods of investigation. Contrary to frequent claims in the applied literature, randomization does not equalize everything other than the treatment in the treatment and control groups; it does not automatically deliver a fair comparison. Instead, randomization provides a probabilistic basis for causal inference, and the credibility of that inference depends on the use of statistical procedures and on background knowledge about the trial setting. These issues matter for the credibility of evidence-based policy, which often relies on trial results taken from settings different from the settings in which policies are to be implemented.",
		},
	},
	"learning": {
		{
			Title:    "The Relationship Between Parental Involvement and Urban Secondary School Student Academic Achievement",
			Authors:  []string{"William H. Jeynes"},
			Year:     2006,
			Venue:    "Urban Education",
			DOI:      "10.1177/0042085906293818",
			Abstract: "A meta-analysis is undertaken, including 52 studies, to determine the influence of parental involvement on the educational outcomes of urban secondary school children. Statistical analyses are done to determine the overall impact of parental involvement as well as specific components of parental involvement. Four different measures of educational outcomes are used. These measures include an overall measure of all components of academic achievement combined, grades, standardized tests, and other measures that generally included teacher rating scales and indices of academic attitudes and behaviors. The possible differing effects of parental involvement by race and socioeconomic status are also examined. The results indicate that the influence of parental involvement overall is significant for secondary school children. Parental involvement as a whole affects all the academic variables under study by about .5 to .55 of a standard deviation unit. The positive effects of parental involvement hold for both White and minority children.",
		},
		{
			Title:    "Effects of parental involvement on academic achievement: a meta-synthesis",
			Authors:  []string{"S. Wilder"},
			Year:     2013,
			Venue:    "Educational Review",
			DOI:      "10.1080/00131911.2013.780009",
			Abstract: "The impact of parental involvement on student academic achievement has been recognized by teachers, administrators, and policy-makers who consider parental involvement to be one of the integral parts of new educational reforms and initiatives. This study synthesized the results of nine meta-analyses that examined this impact and it identified generalizable findings across these studies. The results indicated that the relationship between parental involvement and academic achievement was positive, regardless of a definition of parental involvement or measure of achievement. Furthermore, the findings revealed that this relationship was strongest if parental involvement was defined as parental expectations for academic achievement of their children. However, the impact of parental involvement on student academic achievement was weakest if parental involvement was defined as homework assistance. Finally, the relationship between parental involvement and academic achievement was found to be consistent across different grade levels and ethnic groups. However, the strength of that relationship varied based on the type of assessment used to measure student achievement.",
		},
		{
			Title:    "Parental School Involvement and Children's Academic Achievement",
			Authors:  []string{"Nancy E. Hill", "Lorraine C. Taylor"},
			Year:     2004,
			Venue:    "Current Directions in Psychological Science",
			DOI:      "10.1111/j.0963-7214.2004.00298.x",
			Abstract: "Developing collaborations between families and schools to promote academic success has a long-standing basis in research and is the focus of numerous programs and policies. We outline some of the mechanisms through which parental school involvement affects achievement and identify how patterns and amounts of involvement vary across cultural, economic, and community contexts and across developmental levels. We propose next steps for research, focusing on the importance of considering students' developmental stages, the context in which involvement takes place, and the multiple perspectives through which involvement may be assessed. Finally, we discuss enhancing involvement in diverse situations.",
		},
		{
			Title:    "Parental Involvement and Students' Academic Achievement: A Growth Modeling Analysis",
			Authors:  []string{"Xitao Fan"},
			Year:     2001,
			Venue:    "The Journal of Experimental Education",
			DOI:      "10.1080/00220970109599497",
			Abstract: "The major research objective of this study was to assess the effect of parental involvement on students' academic growth during the high school years. The National Education Longitudinal Study of 1988 (NELS:88) data were used, and latent growth curve analysis within the framework of structural equation modeling was the major analytic tool. The following are the major findings of the study: (a) Parental involvement appears to be multidimensional; (b) ethnic group samples reported comparable degrees of parental involvement; (c) parents' aspiration for their children's education attainment had a consistent and positive effect on students' academic growth; and (d) the effect, or lack thereof, of parental involvement was consistent across ethnic group samples and across data sources (student vs. parent data). Plausible reasons for the consistent effect of parents' aspirations on students' academic achievement are discussed. The author offers explanations for why some parental involvement dimensions showed negative, though generally small, effects on students' academic growth.",
		},
		{
			Title:    "Effect of Parental Involvement on Children’s Academic Achievement in Chile",
			Authors:  []string{"Laura Lara", "Mahía Saracostti"},
			Year:     2019,
			Venue:    "Frontiers in Psychology",
			DOI:      "10.3389/fpsyg.2019.01464",
			Abstract: "Parental involvement in school has been demonstrated to be a key factor for children's academic outcomes. However, there is a lack of research in Chile, as well as in Latin American countries in general, leaving a gap in the literature about the generalization of findings outside developed and industrialized countries, where most of the research has been done. The present study aims to analyse the associations between parental involvement in school and children's academic achievement. Cluster analysis results from a sample of 498 parents or guardians whose children attended second and third grades in 16 public elementary schools in Chile suggested the existence of three different profiles of parental involvement (high, medium, and low) considering different forms of parental involvement (at home, at school and through the invitations made by the children, the teachers, and the school). Results show that there are differences in children's academic achievement between the parental involvement profiles, indicating children whose parents have a low involvement have lower academic achievement. Findings are in line with international research evidence, suggesting the need to focus on this variable too in Latin American contexts.",
		},
		{
			Title:    "Parental involvement in middle school: A meta-analytic assessment of the strategies that promote achievement.",
			Authors:  []string{"Nancy E. Hill", "Diana F. Tyson"},
			Year:     2009,
			Venue:    "Developmental Psychology",
			DOI:      "10.1037/a0015362",
			Abstract: "Early adolescence is often marked by changes in school context, family relationships, and developmental processes. In the context of these changes, academic performance often declines, while at the same time the long-term implications of academic performance increase. In promoting achievement across elementary and secondary school levels, the significant role of families, family-school relations, and parental involvement in education has been highlighted. Although there is a growing body of literature focusing on parental involvement in education during middle school, this research has not been systematically examined to determine which types of involvement have the strongest relation with achievement. The authors conducted a meta-analysis on the existing research on parental involvement in middle school to determine whether and which types of parental involvement are related to achievement. Across 50 studies, parental involvement was positively associated with achievement, with the exception of parental help with homework. Involvement that reflected academic socialization had the strongest positive association with achievement. Based on the known characteristics of the developmental stage and tasks of adolescence, strategies reflecting academic socialization are most consistent with the developmental stage of early adolescence.",
		},
		{
			Title:    "Effects of Parental Involvement and Family Structure on the Academic Achievement of Adolescents",
			Authors:  []string{"William H. Jeynes"},
			Year:     2005,
			Venue:    "Marriage & Family Review",
			DOI:      "10.1300/j002v37n03_06",
			Abstract: "Using the 1992 NELS data set, this study assessed the effects of three aspects of parental involvement and family structure on the academic achievement of those children. The results indicate that family structure and two of the three aspects of parental involvement were associated with higher adolescent academic achievement, when gender, race, and socioeconomic status are controlled for. Family structure was the single greatest predictor of academic achievement. The extent to which parents discussed school issues and attended school functions also had a positive impact on adolescent academic achievement. Whether a parent checked on a child's homework and checked on his or her friends did not have a positive impact, and sometimes had a negative effect, on academic achievement. The significance of these results is discussed. To the extent that parental family structure is, in itself, partially a measure of parental involvement, the relative influence of family structure and other measures of parental involvement on children's academic achievement is discussed.",
		},
		{
			Title:    "A Structural Equation Model of Parental Involvement, Motivational and Aptitudinal Characteristics, and Academic Achievement",
			Authors:  []string{"Júlio António González-Pienda", "José Carlos Núñez", "Soledad González–Pumariega", "Luis Álvarez"},
			Year:     2002,
			Venue:    "The Journal of Experimental Education",
			DOI:      "10.1080/00220970209599509",
			Abstract: "The authors used the structural equation model (SEM) approach to test a model hypothesizing the influence of parental involvement on students' academic aptitudes, self-concept, and causal attributions, as well as the influence of the 3 variables on academic achievement. The theoretical model was contrasted in a group of 12- to 18-year-old adolescents (N = 261) attending various educational centers. The results indicate that (a) parental involvement had a positive and significant influence on the participant's measured characteristics; (b) causal attribution was not causally related to self-concept or academic achievement when the task involved finding causes for success, but, self-concept and causal attributions were found to be significantly and reciprocally related when the task involved finding causes accounting for failure; (c) self-concept was statistically and predominantly causally related to academic achievement, but not vice versa; and (d) aptitude and self-concept accounted for academic achievement, although the effect of self-concept was predominant. These results suggest that in adolescence, cognitive-affective variables become crucial in accounting for academic behavior.",
		},
	},
}
